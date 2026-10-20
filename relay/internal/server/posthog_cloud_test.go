package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

type fakePostHog struct {
	mu        sync.Mutex
	runStatus string
	log       []string
}

func (f *fakePostHog) set(status string, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runStatus = status
	f.log = append(f.log, lines...)
}

func (f *fakePostHog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer phx_test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/projects/2/tasks/":
		_, _ = w.Write([]byte(`{"results":[
			{"id":"t1","task_number":7,"title":" Fix login ","repository":"acme/app","updated_at":"2026-10-20T10:00:00Z","latest_run":{"id":"r1","status":"` + f.runStatus + `","created_at":"2026-10-20T11:00:00Z"}},
			{"id":"t2","title":"Draft","updated_at":"2026-10-19T10:00:00Z","latest_run":null}
		]}`))
	case "/api/projects/2/tasks/t1/runs/r1/":
		_, _ = w.Write([]byte(`{"id":"r1","task":"t1","status":"` + f.runStatus + `","branch":"fix-login","runtime_adapter":"claude","output":{"pr_url":"https://github.com/acme/app/pull/9"}}`))
	case "/api/projects/2/tasks/t1/runs/r1/session_logs/":
		var offset int
		_ = json.Unmarshal([]byte(r.URL.Query().Get("offset")), &offset)
		page := f.log[min(offset, len(f.log)):]
		_, _ = w.Write([]byte("[" + strings.Join(page, ",") + "]"))
	default:
		http.NotFound(w, r)
	}
}

func cloudLine(eventID, method, params string) string {
	return `{"type":"notification","timestamp":"2026-10-20T11:00:00Z","event_id":"` + eventID + `","notification":{"jsonrpc":"2.0","method":"` + method + `","params":` + params + `}}`
}

func TestPhoneFollowsAPostHogCloudRun(t *testing.T) {
	fake := &fakePostHog{runStatus: "in_progress"}
	fake.set("in_progress",
		cloudLine("1", "session/prompt", `{"prompt":[{"type":"text","text":"fix the login bug"}]}`),
		cloudLine("2", "session/update", `{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"Looking."}}}`),
	)
	api := httptest.NewServer(fake)
	defer api.Close()
	configPath := filepath.Join(t.TempDir(), "posthog-cloud.json")
	relay := startRelayWith(t, func(options *Options) {
		options.PostHogCloudConfig = configPath
		options.PostHogCloudHTTP = api.Client()
		options.PollInterval.PostHogCloud = 20 * time.Millisecond
	})
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	welcome := resultFor(t, ctx, conn, "1")
	if features := welcome["data"].(map[string]any)["features"].([]any); !containsAny(features, postHogCloudFeature) {
		t.Fatalf("the welcome must offer PostHog Cloud: %v", features)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_tasks", "id": "2"})
	if data := resultFor(t, ctx, conn, "2")["data"].(map[string]any); data["connected"] != false {
		t.Fatalf("a computer without a key must say it isn't connected: %v", data)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "watch_posthog_cloud_run", "id": "3", "task_id": "t1", "run_id": "r1"})
	if result := resultFor(t, ctx, conn, "3"); errorCode(result) != "posthog_cloud_not_connected" {
		t.Fatalf("watching without a key must be refused: %v", result)
	}

	if err := posthogcloud.SaveConfig(configPath, posthogcloud.Config{Host: api.URL, ProjectID: 2, APIKey: "phx_test", UserID: 42, Email: "me@example.com"}); err != nil {
		t.Fatal(err)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_tasks", "id": "4"})
	data := resultFor(t, ctx, conn, "4")["data"].(map[string]any)
	tasks := data["tasks"].([]any)
	if data["connected"] != true || data["account"] != "me@example.com" || len(tasks) != 2 {
		t.Fatalf("tasks = %v", data)
	}
	first, second := tasks[0].(map[string]any), tasks[1].(map[string]any)
	if first["title"] != "Fix login" || first["run_id"] != "r1" || first["status"] != "in_progress" || first["updated_at"] != "2026-10-20T11:00:00Z" {
		t.Fatalf("a task must carry its latest run: %v", first)
	}
	if first["url"] != api.URL+"/project/2/tasks/t1" {
		t.Fatalf("a task must link to PostHog: %v", first["url"])
	}
	if second["status"] != "not_started" || second["run_id"] != nil {
		t.Fatalf("a task with no run must say so: %v", second)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "watch_posthog_cloud_run", "id": "5", "task_id": "t1", "run_id": "r1"})
	if result := resultFor(t, ctx, conn, "5"); result["ok"] != true {
		t.Fatalf("watch = %v", result)
	}
	chat := readUntil(t, ctx, conn, "posthog_cloud_chat")
	if chat["reset"] != true || len(chat["entries"].([]any)) != 2 {
		t.Fatalf("the first chat message must carry the whole log: %v", chat)
	}
	run := readUntil(t, ctx, conn, "posthog_cloud_run")["run"].(map[string]any)
	if run["status"] != "in_progress" || run["state"] != "working" || run["pull_request"] != "https://github.com/acme/app/pull/9" || run["agent"] != "claude" {
		t.Fatalf("run = %v", run)
	}

	fake.set("in_progress", cloudLine("3", "session/update", `{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"Fixed it."}}}`))
	chat = readUntil(t, ctx, conn, "posthog_cloud_chat")
	entries := chat["entries"].([]any)
	if chat["reset"] != false || len(entries) != 1 || entries[0].(map[string]any)["text"] != "Fixed it." {
		t.Fatalf("new log lines must arrive as changes: %v", chat)
	}

	fake.set("completed", cloudLine("4", "_posthog/turn_complete", `{"stopReason":"end_turn"}`))
	chat = readUntil(t, ctx, conn, "posthog_cloud_chat")
	if chat["reset"] != true || len(chat["entries"].([]any)) != 3 {
		t.Fatalf("a finished run must be sent whole once more: %v", chat)
	}
	run = readUntil(t, ctx, conn, "posthog_cloud_run")["run"].(map[string]any)
	if run["status"] != "completed" || run["state"] != nil {
		t.Fatalf("a finished run carries no turn state: %v", run)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "unwatch_posthog_cloud_run", "id": "6"})
	if result := resultFor(t, ctx, conn, "6"); result["ok"] != true {
		t.Fatalf("unwatch = %v", result)
	}
}

func containsAny(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
