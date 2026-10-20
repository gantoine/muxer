package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

type fakePostHog struct {
	mu         sync.Mutex
	runStatus  string
	listStatus string
	log        []string
	streams    int
	events     chan string
	commands   []fakeCommand
	notReady   map[string]int
	resumed    int
}

type fakeCommand struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func (f *fakePostHog) sent(method string) []fakeCommand {
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

func (f *fakePostHog) waitForCommand(t *testing.T, method string, count int) []fakeCommand {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sent := f.sent(method); len(sent) >= count {
			return sent
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PostHog never got %d %s commands: %+v", count, method, f.sent(method))
	return nil
}

func newFakePostHog(lines ...string) *fakePostHog {
	return &fakePostHog{runStatus: "in_progress", log: lines, events: make(chan string, 16), notReady: map[string]int{}}
}

func (f *fakePostHog) set(status string, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runStatus = status
	f.log = append(f.log, lines...)
}

func (f *fakePostHog) streamCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams
}

func (f *fakePostHog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer phx_test" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	run := "/api/projects/2/tasks/t1/runs/r1/"
	if r.URL.Path == run+"stream/" {
		f.serveStream(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/projects/2/tasks/":
		_, _ = w.Write([]byte(`{"results":[
			{"id":"t1","task_number":7,"title":" Fix login ","repository":"acme/app","updated_at":"2026-10-20T10:00:00Z","latest_run":{"id":"r1","status":"` + cmp.Or(f.listStatus, f.runStatus) + `","created_at":"2026-10-20T11:00:00Z"}},
			{"id":"t2","title":"Draft","updated_at":"2026-10-19T10:00:00Z","latest_run":null}
		]}`))
	case run:
		_, _ = w.Write([]byte(`{"id":"r1","task":"t1","status":"` + f.runStatus + `","branch":"fix-login","runtime_adapter":"claude","output":{"pr_url":"https://github.com/acme/app/pull/9"},"state":{"claude_subscription_user_id":42}}`))
	case run + "command/":
		var command fakeCommand
		_ = json.NewDecoder(r.Body).Decode(&command)
		f.commands = append(f.commands, command)
		if f.notReady[command.Method] > 0 {
			f.notReady[command.Method]--
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"The sandbox is starting","code":"sandbox_not_ready"}`))
			return
		}
		result := `{"queued":true}`
		if command.Method == "permission_response" {
			result = `{"resolved":true}`
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":` + result + `}`))
	case run + "resume_in_cloud/":
		f.resumed++
		f.runStatus = "in_progress"
		_, _ = w.Write([]byte(`{"id":"r1","task":"t1","status":"in_progress"}`))
	case run + "session_logs/":
		var offset int
		_ = json.Unmarshal([]byte(r.URL.Query().Get("offset")), &offset)
		page := f.log[min(offset, len(f.log)):]
		_, _ = w.Write([]byte("[" + strings.Join(page, ",") + "]"))
	case run + "stream_token/":
		_, _ = w.Write([]byte(`{"token":"stream","stream_base_url":null}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakePostHog) serveStream(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.streams++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-f.events:
			_, _ = w.Write([]byte(event))
			w.(http.Flusher).Flush()
		}
	}
}

func cloudLine(eventID, method, params string) string {
	return `{"type":"notification","timestamp":"2026-10-20T11:00:00Z","event_id":"` + eventID + `","notification":{"jsonrpc":"2.0","method":"` + method + `","params":` + params + `}}`
}

func cloudEvent(id, data string) string {
	return "id: " + id + "\ndata: " + data + "\n\n"
}

func waitForRun(t *testing.T, ctx context.Context, conn *websocket.Conn, match func(map[string]any) bool) map[string]any {
	t.Helper()
	for {
		run := readUntil(t, ctx, conn, "posthog_cloud_run")["run"].(map[string]any)
		if match(run) {
			return run
		}
	}
}

func TestPhoneFollowsAPostHogCloudRunLive(t *testing.T) {
	fake := newFakePostHog(
		cloudLine("b-1", "session/prompt", `{"prompt":[{"type":"text","text":"fix the login bug"}]}`),
		cloudLine("b-2", "session/update", `{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"Looking."}}}`),
	)
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)
	configPath := filepath.Join(t.TempDir(), "posthog-cloud.json")
	relay := startRelayWith(t, func(options *Options) {
		options.PostHogCloudConfig = configPath
		options.PostHogCloudHTTP = api.Client()
		options.PollInterval.PostHogCloud = 20 * time.Millisecond
		options.PostHogCloudFollower = posthogcloud.FollowerOptions{RetryBase: 10 * time.Millisecond}
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
	deadline := time.Now().Add(5 * time.Second)
	var first map[string]any
	for id := 4; ; id++ {
		_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_tasks", "id": fmt.Sprint(id)})
		data := resultFor(t, ctx, conn, fmt.Sprint(id))["data"].(map[string]any)
		if data["connected"] != true || data["account"] != "me@example.com" || len(data["tasks"].([]any)) != 2 {
			t.Fatalf("tasks = %v", data)
		}
		first = data["tasks"].([]any)[0].(map[string]any)
		if first["state"] == "working" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first["title"] != "Fix login" || first["run_id"] != "r1" || first["status"] != "in_progress" || first["state"] != "working" {
		t.Fatalf("an in-progress task must carry its live turn state from the background watch: %v", first)
	}
	if first["url"] != api.URL+"/project/2/tasks/t1" {
		t.Fatalf("a task must link to PostHog: %v", first["url"])
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "watch_posthog_cloud_run", "id": "watch", "task_id": "t1", "run_id": "r1"})
	if result := resultFor(t, ctx, conn, "watch"); result["ok"] != true {
		t.Fatalf("watch = %v", result)
	}
	chat := readUntil(t, ctx, conn, "posthog_cloud_chat")
	if chat["reset"] != true || len(chat["entries"].([]any)) != 2 {
		t.Fatalf("the first chat message must carry the whole history: %v", chat)
	}
	run := waitForRun(t, ctx, conn, func(run map[string]any) bool { return run["state"] == "working" })
	if run["pull_request"] != "https://github.com/acme/app/pull/9" || run["agent"] != "claude" {
		t.Fatalf("run = %v", run)
	}
	if count := fake.streamCount(); count != 1 {
		t.Fatalf("the phone and the background watch must share one stream, got %d", count)
	}

	fake.events <- cloudEvent("s-1", cloudLine("b-3", "session/update", `{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Found it"}}}`))
	chat = readUntil(t, ctx, conn, "posthog_cloud_chat")
	entries := chat["entries"].([]any)
	if chat["reset"] != false || len(entries) != 1 || entries[0].(map[string]any)["text"] != "Found it" {
		t.Fatalf("live events must arrive as changes: %v", chat)
	}

	fake.events <- cloudEvent("s-2", `{"type":"permission_request","requestId":"q1","toolCall":{"toolCallId":"c1","title":"Run the tests"},"options":[{"optionId":"allow","kind":"allow_once"}]}`)
	waitForRun(t, ctx, conn, func(run map[string]any) bool { return run["state"] == "waiting" })

	fake.set("completed",
		cloudLine("b-3", "session/update", `{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"Found it and fixed it."}}}`),
		cloudLine("b-4", "_posthog/turn_complete", `{"stopReason":"end_turn"}`),
	)
	fake.events <- cloudEvent("s-3", `{"type":"task_run_state","run_id":"r1","status":"completed"}`)
	for {
		chat = readUntil(t, ctx, conn, "posthog_cloud_chat")
		if chat["reset"] == true {
			break
		}
	}
	entries = chat["entries"].([]any)
	if len(entries) != 3 || entries[2].(map[string]any)["text"] != "Found it and fixed it." {
		t.Fatalf("a finished run must be sent whole once more: %v", entries)
	}
	run = waitForRun(t, ctx, conn, func(run map[string]any) bool { return run["status"] == "completed" })
	if run["state"] != nil {
		t.Fatalf("a finished run carries no turn state: %v", run)
	}

	fake.mu.Lock()
	fake.listStatus = "in_progress"
	fake.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_tasks", "id": "after"})
	task := resultFor(t, ctx, conn, "after")["data"].(map[string]any)["tasks"].([]any)[0].(map[string]any)
	if task["status"] != "completed" || task["state"] != nil {
		t.Fatalf("a lagging task list must take the followed run's status: %v", task)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "unwatch_posthog_cloud_run", "id": "unwatch"})
	if result := resultFor(t, ctx, conn, "unwatch"); result["ok"] != true {
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
