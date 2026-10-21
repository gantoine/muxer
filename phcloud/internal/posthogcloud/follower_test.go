package posthogcloud

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const holdOpen = "<hold>"

type scriptedAPI struct {
	t       *testing.T
	mu      sync.Mutex
	status  string
	logs    []string
	scripts chan []string
	opened  []http.Header
	queries []string
}

func newScriptedAPI(t *testing.T, logs ...string) *scriptedAPI {
	return &scriptedAPI{t: t, status: "in_progress", logs: logs, scripts: make(chan []string, 8)}
}

func (a *scriptedAPI) set(status string, logs ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
	a.logs = append(a.logs, logs...)
}

func (a *scriptedAPI) connections() ([]http.Header, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]http.Header(nil), a.opened...), append([]string(nil), a.queries...)
}

func (a *scriptedAPI) waitForConnections(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if headers, _ := a.connections(); len(headers) >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the follower never opened stream %d", count)
}

func (a *scriptedAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	base := "/api/projects/2/tasks/t1/runs/r1/"
	switch r.URL.Path {
	case base:
		a.mu.Lock()
		fmt.Fprintf(w, `{"id":"r1","task":"t1","status":%q}`, a.status)
		a.mu.Unlock()
	case base + "session_logs/":
		a.mu.Lock()
		page := "[" + strings.Join(a.logs, ",") + "]"
		a.mu.Unlock()
		if r.URL.Query().Get("offset") != "0" {
			page = "[]"
		}
		_, _ = w.Write([]byte(page))
	case base + "stream_token/":
		_, _ = w.Write([]byte(`{"token":"t","stream_base_url":null}`))
	case base + "stream/":
		a.mu.Lock()
		a.opened = append(a.opened, r.Header.Clone())
		a.queries = append(a.queries, r.URL.RawQuery)
		a.mu.Unlock()
		var script []string
		select {
		case script = <-a.scripts:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range script {
			if chunk == holdOpen {
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
		}
	default:
		http.NotFound(w, r)
	}
}

func sse(id, data string) string {
	return "id: " + id + "\ndata: " + data + "\n\n"
}

func stored(eventID, firstEventID, method, params string) string {
	first := ""
	if firstEventID != "" {
		first = `,"first_event_id":"` + firstEventID + `"`
	}
	return `{"type":"notification","timestamp":"T","event_id":"` + eventID + `"` + first + `,"notification":{"jsonrpc":"2.0","method":"` + method + `","params":` + params + `}}`
}

func message(kind, text string) string {
	return `{"update":{"sessionUpdate":"` + kind + `","content":{"type":"text","text":"` + text + `"}}}`
}

type recorder struct {
	mu      sync.Mutex
	updates []Update
	signal  chan struct{}
}

func newRecorder() *recorder {
	return &recorder{signal: make(chan struct{}, 100)}
}

func (r *recorder) record(update Update) {
	r.mu.Lock()
	r.updates = append(r.updates, update)
	r.mu.Unlock()
	r.signal <- struct{}{}
}

func (r *recorder) waitFor(t *testing.T, what string, match func(Update) bool) Update {
	t.Helper()
	deadline := time.After(5 * time.Second)
	seen := 0
	for {
		r.mu.Lock()
		updates := append([]Update(nil), r.updates...)
		r.mu.Unlock()
		for ; seen < len(updates); seen++ {
			if match(updates[seen]) {
				return updates[seen]
			}
		}
		select {
		case <-r.signal:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; updates: %+v", what, updates)
		}
	}
}

func startFollower(t *testing.T, api *scriptedAPI) (*recorder, chan error) {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	client := NewClient(Config{Host: server.URL, ProjectID: 2, APIKey: "k"}, server.Client())
	updates := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	follower := NewFollower(client, "t1", "r1", FollowerOptions{IdleTimeout: 2 * time.Second, RetryBase: 10 * time.Millisecond}, updates.record)
	go func() { done <- follower.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return updates, done
}

func TestFollowerSkipsWhatTheHistoryCoversAndResumesAfterRotation(t *testing.T) {
	api := newScriptedAPI(t,
		stored("b-1", "", "session/prompt", `{"prompt":[{"type":"text","text":"fix it"}]}`),
		stored("b-4", "b-2", "session/update", message("agent_message", "Looking now.")),
	)
	api.scripts <- []string{
		sse("r-2", stored("b-2", "", "session/update", message("agent_message_chunk", "Looking"))),
		sse("r-3", stored("b-3", "", "session/update", message("agent_message_chunk", " now."))),
		": comment\n\n",
		"event: keepalive\ndata: {}\n\n",
		sse("r-5", stored("b-5", "", "session/update", message("agent_message_chunk", "Fixed"))),
		sse("r-5", stored("b-5", "", "session/update", message("agent_message_chunk", "Fixed"))),
		"event: end\ndata: {\"type\":\"rotated\"}\n\n",
	}
	updates, done := startFollower(t, api)

	reset := updates.waitFor(t, "the history", func(u Update) bool { return u.Reset })
	if len(reset.Entries) != 2 || reset.Entries[1].Text != "Looking now." || reset.Entries[1].ID != "b-2" {
		t.Fatalf("reset = %+v", reset.Entries)
	}
	live := updates.waitFor(t, "a live message", func(u Update) bool { return len(u.Entries) > 0 && !u.Reset })
	if len(live.Entries) != 1 || live.Entries[0].Text != "Fixed" || live.Entries[0].ID != "b-5" {
		t.Fatalf("covered chunks must be skipped and new ones applied once: %+v", live.Entries)
	}

	api.waitForConnections(t, 2)
	api.set("completed", stored("b-6", "b-5", "session/update", message("agent_message", "Fixed it.")))
	api.scripts <- []string{sse("r-6", `{"type":"task_run_state","run_id":"r1","status":"completed"}`)}
	final := updates.waitFor(t, "the final history", func(u Update) bool { return u.Reset && u.Run != nil && u.Run.Status == "completed" })
	if last := final.Entries[len(final.Entries)-1]; last.Text != "Fixed it." || last.ID != "b-5" {
		t.Fatalf("the final reload must keep the live message's id: %+v", final.Entries)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a finished run must end the follower cleanly: %v", err)
		}
		done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("the follower kept going after the run finished")
	}
	headers, queries := api.connections()
	if len(headers) != 2 || queries[0] != "start=latest" || headers[1].Get("Last-Event-ID") != "r-5" || queries[1] != "" {
		t.Fatalf("the reconnect must resume from the last event: %v %v", queries, headers)
	}
}

func TestFollowerRebuildsAfterAResyncAndRetriesErrors(t *testing.T) {
	api := newScriptedAPI(t, stored("b-1", "", "session/prompt", `{"prompt":[{"type":"text","text":"clean up"}]}`))
	api.scripts <- []string{
		sse("r-2", `{"type":"permission_request","requestId":"q1","toolCall":{"toolCallId":"c1","title":"rm -rf build"},"options":[{"optionId":"allow","kind":"allow_once"}]}`),
		"event: end\ndata: {\"type\":\"resync\"}\n\n",
	}
	updates, _ := startFollower(t, api)
	waiting := updates.waitFor(t, "the approval", func(u Update) bool { return u.State == StateWaiting })
	if len(waiting.Entries) != 1 || waiting.Entries[0].Text != "Waiting for approval: rm -rf build" {
		t.Fatalf("waiting = %+v", waiting)
	}

	api.waitForConnections(t, 2)
	api.set("in_progress", stored("b-3", "", "session/update", message("agent_message", "Done cleaning.")))
	api.scripts <- []string{"event: error\ndata: {\"error\":\"backlog unavailable\"}\n\n"}
	failed := updates.waitFor(t, "the stream error", func(u Update) bool { return u.Err != nil })
	if !strings.Contains(failed.Err.Error(), "backlog unavailable") {
		t.Fatalf("err = %v", failed.Err)
	}
	api.scripts <- []string{holdOpen}
	api.waitForConnections(t, 3)
	rebuilt := updates.waitFor(t, "the rebuilt history", func(u Update) bool { return u.Reset && len(u.Entries) == 2 })
	if rebuilt.Entries[1].Text != "Done cleaning." {
		t.Fatalf("rebuilt = %+v", rebuilt.Entries)
	}
	headers, queries := api.connections()
	if len(headers) < 2 || queries[1] != "start=latest" || headers[1].Get("Last-Event-ID") != "" {
		t.Fatalf("a resync must start over from the latest event: %v %v", queries, headers)
	}
	if len(headers) < 3 || headers[2].Get("Last-Event-ID") != "" {
		t.Fatalf("an error before any event keeps the cursor empty: %v", headers)
	}
}
