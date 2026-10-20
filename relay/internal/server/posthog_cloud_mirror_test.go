package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

type mirrorTask struct {
	ID, RunID, Title, Repository, Status, CompletedAt string
}

type fakeTasksAPI struct {
	mu       sync.Mutex
	tasks    []mirrorTask
	commands []fakeCommand
}

func (f *fakeTasksAPI) setTasks(tasks ...mirrorTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = tasks
}

func (f *fakeTasksAPI) sent(method string) []fakeCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matching []fakeCommand
	for _, command := range f.commands {
		if command.Method == method {
			matching = append(matching, command)
		}
	}
	return matching
}

func (f *fakeTasksAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/projects/2/tasks/" {
		var rows []map[string]any
		for _, task := range f.tasks {
			rows = append(rows, map[string]any{"id": task.ID, "title": task.Title, "repository": task.Repository, "created_at": "2026-10-20T09:00:00Z",
				"latest_run": map[string]any{"id": task.RunID, "status": task.Status, "completed_at": task.CompletedAt}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 7 {
		http.NotFound(w, r)
		return
	}
	runID := parts[6]
	status := ""
	for _, task := range f.tasks {
		if task.RunID == runID {
			status = task.Status
		}
	}
	switch strings.Join(parts[7:], "/") {
	case "":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": runID, "status": status, "runtime_adapter": "codex"})
	case "session_logs":
		_, _ = w.Write([]byte("[" + cloudLine(runID+"-1", "session/prompt", `{"prompt":[{"type":"text","text":"do the thing"}]}`) + "]"))
	case "stream_token":
		_, _ = w.Write([]byte(`{"token":"stream","stream_base_url":null}`))
	case "stream":
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		f.mu.Lock()
	case "command":
		var command fakeCommand
		_ = json.NewDecoder(r.Body).Decode(&command)
		f.commands = append(f.commands, command)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"queued":true}}`))
	default:
		http.NotFound(w, r)
	}
}

func waitUntil(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloudRunsBecomeCmuxWorkspaces(t *testing.T) {
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	api := &fakeTasksAPI{}
	api.setTasks(
		mirrorTask{ID: "t1", RunID: "r1", Title: "Fix login", Repository: "acme/app", Status: "in_progress"},
		mirrorTask{ID: "t2", RunID: "r2", Title: "Bump deps", Repository: "acme/app", Status: "completed", CompletedAt: recent},
		mirrorTask{ID: "t3", RunID: "r3", Title: "Ancient", Repository: "acme/app", Status: "completed", CompletedAt: old},
		mirrorTask{ID: "t4", RunID: "r4", Title: "Draft notes", Status: "in_progress"},
	)
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "posthog-cloud.json")
	if err := posthogcloud.SaveConfig(configPath, posthogcloud.Config{Host: server.URL, ProjectID: 2, APIKey: "k", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	relay := startRelayWith(t, func(options *Options) {
		options.PostHogCloudConfig = configPath
		options.PostHogCloudHTTP = server.Client()
		options.PostHogCloudMirror = filepath.Join(dir, "posthog-cloud-workspaces.json")
		options.PostHogCloudExecutable = "/opt/muxer"
		options.PollInterval.PostHogCloud = 30 * time.Millisecond
		options.PostHogCloudFollower = posthogcloud.FollowerOptions{RetryBase: 10 * time.Millisecond}
	})
	fake := relay.cmux
	fake.mu.Lock()
	fake.growWorkspaces = true
	fake.workspaceList = &cmux.WorkspaceList{
		Workspaces: []cmux.Workspace{{ID: "w1", Title: "api", Terminals: []cmux.Terminal{{ID: "t1", Title: "zsh"}}}},
		Groups:     []cmux.WorkspaceGroup{{ID: "g9", Name: "acme/app", MemberWorkspaceIDs: []string{"w1"}}},
	}
	fake.performResults = map[string]string{cmux.MethodGroupCreate: `{"group":{"id":"g10"}}`}
	fake.mu.Unlock()
	snapshot := func() (workspaces []envWorkspace, commands, performed, closed []string) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return slices.Clone(fake.envWorkspaces), slices.Clone(fake.commands), slices.Clone(fake.performed), slices.Clone(fake.closed)
	}
	waitUntil(t, "three workspaces and their viewers", func() bool {
		workspaces, commands, _, _ := snapshot()
		return len(workspaces) == 3 && len(commands) == 3
	})
	workspaces, commands, performed, _ := snapshot()
	var titles []string
	for _, workspace := range workspaces {
		titles = append(titles, workspace.title)
	}
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"Bump deps", "Draft notes", "Fix login"}) {
		t.Fatalf("only active and recent runs get a workspace: %v", titles)
	}
	joined := strings.Join(commands, "\n")
	if !strings.Contains(joined, `clear; '/opt/muxer' posthog-cloud attach --task 't1' --run 'r1'`) {
		t.Fatalf("each workspace must run the viewer: %v", commands)
	}
	actions := strings.Join(performed, "\n")
	if strings.Count(actions, cmux.MethodGroupAdd+` {"group_id":"g10"`) != 3 {
		t.Fatalf("every run must join its repository's group as a member: %v", performed)
	}
	if !strings.Contains(actions, `"idempotency_key":"posthog-cloud:acme/app","name":"acme/app"`) || !strings.Contains(actions, `"idempotency_key":"posthog-cloud:PostHog Cloud","name":"PostHog Cloud"`) {
		t.Fatalf("groups must be created empty and keyed by repository: %v", performed)
	}
	if strings.Contains(actions, "child_workspace_ids") {
		t.Fatalf("a run must never become a group's anchor: %v", performed)
	}
	if !strings.Contains(actions, `"description":"PostHog Cloud · acme/app"`) {
		t.Fatalf("workspaces must say where they come from: %v", performed)
	}

	var login, deps, notes string
	for runID, mirrored := range relay.server.mirror.snapshot() {
		switch runID {
		case "r1":
			login = mirrored.SurfaceID
		case "r2":
			deps = mirrored.WorkspaceID
		case "r4":
			notes = mirrored.WorkspaceID
		}
	}
	waitUntil(t, "the lanes", func() bool {
		_, _, performed, _ := snapshot()
		actions := strings.Join(performed, "\n")
		return strings.Contains(actions, `"status":"done","workspace_id":"`+deps+`"`) && strings.Count(actions, `"status":"working"`) >= 2
	})

	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "hello"})
	resultFor(t, ctx, conn, "hello")
	listing := readMatching(t, ctx, conn, "the decorated terminal", func(message map[string]any) bool {
		return message["type"] == "workspaces" && strings.Contains(fmt.Sprint(message["workspaces"]), "agent_source:codex")
	})
	if !strings.Contains(fmt.Sprint(listing["workspaces"]), "agent_state:Running") {
		t.Fatalf("a mirrored terminal must read as a running agent: %v", listing["workspaces"])
	}

	workspaceOf := func(surfaceID string) string {
		for _, mirrored := range relay.server.mirror.snapshot() {
			if mirrored.SurfaceID == surfaceID {
				return mirrored.WorkspaceID
			}
		}
		return ""
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "id": "attach", "workspace_id": workspaceOf(login), "surface_id": login})
	resultFor(t, ctx, conn, "attach")
	session := readMatching(t, ctx, conn, "the cloud chat session", func(message map[string]any) bool {
		return message["type"] == "chat_session" && message["surface_id"] == login
	})
	if session["session_id"] != "r1" {
		t.Fatalf("a mirrored terminal's chat is its run: %v", session)
	}
	chat := readMatching(t, ctx, conn, "the cloud chat", func(message map[string]any) bool {
		return message["type"] == "chat" && message["surface_id"] == login && message["reset"] == true
	})
	if entries := chat["entries"].([]any); len(entries) != 1 || entries[0].(map[string]any)["text"] != "do the thing" {
		t.Fatalf("chat = %v", chat)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "paste", "id": "reply", "text": "and the signup page", "submit": true})
	if result := resultFor(t, ctx, conn, "reply"); result["ok"] != true {
		t.Fatalf("a reply in a mirrored terminal must reach the run: %v", result)
	}
	if sent := api.sent("user_message"); len(sent) != 1 || sent[0].Params["content"] != "and the signup page" {
		t.Fatalf("user_message = %+v", sent)
	}
	_, commandsBefore, _, _ := snapshot()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "key", "id": "esc", "key": "escape"})
	resultFor(t, ctx, conn, "esc")
	if len(api.sent("cancel")) != 1 {
		t.Fatalf("Esc in a mirrored terminal must stop the agent")
	}
	if _, commandsAfter, _, _ := snapshot(); len(commandsAfter) != len(commandsBefore) {
		t.Fatalf("Esc must not reach the viewer")
	}

	fake.mu.Lock()
	fake.workspaceList.Workspaces = slices.DeleteFunc(fake.workspaceList.Workspaces, func(workspace cmux.Workspace) bool { return workspace.ID == notes })
	fake.mu.Unlock()
	api.setTasks(
		mirrorTask{ID: "t1", RunID: "r1", Title: "Fix login", Repository: "acme/app", Status: "in_progress"},
		mirrorTask{ID: "t2", RunID: "r2", Title: "Bump deps", Repository: "acme/app", Status: "completed", CompletedAt: old},
		mirrorTask{ID: "t4", RunID: "r4", Title: "Draft notes", Status: "in_progress"},
	)
	waitUntil(t, "the expired workspace to close", func() bool {
		_, _, _, closed := snapshot()
		return slices.Contains(closed, deps)
	})
	time.Sleep(100 * time.Millisecond)
	if workspaces, _, _, _ := snapshot(); len(workspaces) != 3 {
		t.Fatalf("a workspace you closed must not come back: %d created", len(workspaces))
	}
	if _, ok := relay.server.mirror.run("r4"); ok {
		t.Fatalf("a closed workspace must be forgotten")
	}
}

func TestMirrorRulesAndLanes(t *testing.T) {
	now := time.Now()
	task := func(status, completed string) posthogcloud.Task {
		return posthogcloud.Task{LatestRun: &posthogcloud.RunSummary{ID: "r", Status: status, CompletedAt: completed}}
	}
	if !wanted(task("queued", ""), now) || wanted(task("not_started", ""), now) {
		t.Fatal("queued runs are mirrored, unstarted ones aren't")
	}
	if !wanted(task("failed", now.Add(-23*time.Hour).Format(time.RFC3339)), now) || wanted(task("completed", now.Add(-25*time.Hour).Format(time.RFC3339)), now) {
		t.Fatal("finished runs stay for 24 hours")
	}
	cases := map[[2]string]string{
		{"in_progress", posthogcloud.StateWorking}: "working",
		{"in_progress", posthogcloud.StateWaiting}: "needs-attention",
		{"in_progress", posthogcloud.StateIdle}:    "review",
		{"failed", ""}:                             "needs-attention",
		{"completed", ""}:                          "done",
	}
	for input, lane := range cases {
		if got := laneFor(input[0], input[1]); got != lane {
			t.Errorf("laneFor(%v) = %s, want %s", input, got, lane)
		}
	}
	if groupName("") != "PostHog Cloud" || groupName(" acme/app ") != "acme/app" {
		t.Fatal("groupName")
	}
	if shellQuote("it's") != `'it'\''s'` {
		t.Fatal("shellQuote")
	}
}
