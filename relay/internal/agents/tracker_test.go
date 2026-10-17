package agents

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

type fakeSource struct {
	listed []cmux.AgentMessage
	sent   []string
}

func (f *fakeSource) SendAgentMessage(_ context.Context, target, from, body string) (cmux.AgentMessage, error) {
	f.sent = append(f.sent, target+"|"+from+"|"+body)
	return cmux.AgentMessage{ID: "m1", SenderName: from, Body: body, State: "queued"}, nil
}

func (f *fakeSource) AgentMessages(context.Context, string, int) ([]cmux.AgentMessage, error) {
	return f.listed, nil
}

var start = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func newTestTracker(source *fakeSource) *Tracker {
	tracker := NewTracker(source, slog.New(slog.DiscardHandler))
	tracker.now = func() time.Time { return start }
	return tracker
}

func event(name, surfaceID string, payload map[string]any) cmux.Event {
	encoded, _ := json.Marshal(payload)
	workspaceID := "w1"
	return cmux.Event{Type: "event", Name: name, SurfaceID: &surfaceID, WorkspaceID: &workspaceID, Payload: encoded}
}

func received(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func TestScheduledResumeBecomesARetryUntilCmuxSendsIt(t *testing.T) {
	tracker := newTestTracker(&fakeSource{})
	changes, unsubscribe := tracker.SubscribeActivity()
	defer unsubscribe()

	tracker.HandleEvent(context.Background(), event("agent.auto_resume.scheduled", "s1", map[string]any{"agent": "claude", "attempt": 2, "delay_ms": 45000}))
	retries := tracker.Retries()
	if len(retries) != 1 || retries[0].Attempt != 2 || retries[0].Agent != "claude" || retries[0].WorkspaceID != "w1" {
		t.Fatalf("retries = %+v", retries)
	}
	if !retries[0].DueAt.Equal(start.Add(45 * time.Second)) {
		t.Fatalf("due at %v, want 45s after the event", retries[0].DueAt)
	}
	if !received(changes) {
		t.Fatal("subscribers must hear about a new retry")
	}

	tracker.HandleEvent(context.Background(), event("agent.auto_resume.sent", "s1", map[string]any{"agent": "claude", "attempt": 2, "total": 2}))
	if len(tracker.Retries()) != 0 || !received(changes) {
		t.Fatal("a sent resume must clear the retry and tell subscribers")
	}
}

func TestAgentActivityCancelsAPendingRetry(t *testing.T) {
	tracker := newTestTracker(&fakeSource{})
	tracker.HandleEvent(context.Background(), event("agent.auto_resume.scheduled", "s1", map[string]any{"attempt": 1, "delay_ms": 20000}))

	tracker.HandleEvent(context.Background(), event("agent.hook.Notification", "s1", nil))
	tracker.HandleEvent(context.Background(), event("agent.hook.UserPromptSubmit", "s2", nil))
	if len(tracker.Retries()) != 1 {
		t.Fatal("notifications and other terminals must not cancel the retry")
	}

	tracker.HandleEvent(context.Background(), event("agent.hook.UserPromptSubmit", "s1", nil))
	if len(tracker.Retries()) != 0 {
		t.Fatal("a new prompt on the terminal cancels the retry, as cmux does")
	}
}

func TestMessageEventsWakeOnlyWatchersOfThatTerminal(t *testing.T) {
	tracker := newTestTracker(&fakeSource{})
	watching, stopWatching := tracker.SubscribeMessages("s1")
	defer stopWatching()
	other, stopOther := tracker.SubscribeMessages("s2")
	defer stopOther()

	tracker.HandleEvent(context.Background(), event("agent.message.delivered", "s1", map[string]any{"id": "m1"}))
	if !received(watching) || received(other) {
		t.Fatal("only the recipient terminal's watchers should refresh")
	}
}

func TestMessagesListOnlyWhatPhonesSent(t *testing.T) {
	source := &fakeSource{listed: []cmux.AgentMessage{
		{ID: "a", SenderName: "Muxer on Pixel"},
		{ID: "b", SenderName: "reviewer"},
		{ID: "c", SenderName: "Muxer on Tablet"},
	}}
	messages, err := newTestTracker(source).Messages(context.Background(), "s1")
	if err != nil || len(messages) != 2 || messages[0].ID != "a" || messages[1].ID != "c" {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
}

func TestSendNamesThePhoneAndRejectsEmptyText(t *testing.T) {
	source := &fakeSource{}
	tracker := newTestTracker(source)
	if _, err := tracker.Send(context.Background(), "s1", "Pixel 10", "  \n"); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("blank text must be rejected, got %v", err)
	}
	if _, err := tracker.Send(context.Background(), "s1", "Pixel\n10", "use the staging db"); err != nil {
		t.Fatal(err)
	}
	if len(source.sent) != 1 || source.sent[0] != "s1|Muxer on Pixel 10|use the staging db" {
		t.Fatalf("sent = %v", source.sent)
	}
	if long := SenderName(strings.Repeat("x", 100)); len([]rune(long)) != 64 {
		t.Fatalf("sender names are capped at 64 characters, got %d", len([]rune(long)))
	}
}

func TestHooksTrackWhetherTheAgentIsMidTurn(t *testing.T) {
	tracker := newTestTracker(&fakeSource{})
	changes, unsubscribe := tracker.SubscribeActivity()
	defer unsubscribe()

	tracker.HandleEvent(context.Background(), event("agent.hook.UserPromptSubmit", "s1", nil))
	if busy := tracker.Busy(); len(busy) != 1 || busy[0] != "s1" || !received(changes) {
		t.Fatalf("a submitted prompt makes the agent busy: %v", busy)
	}
	tracker.HandleEvent(context.Background(), event("agent.hook.PreToolUse", "s1", nil))
	if received(changes) {
		t.Fatal("staying busy is not a change")
	}
	tracker.HandleEvent(context.Background(), event("agent.hook.Notification", "s1", nil))
	tracker.HandleEvent(context.Background(), event("agent.hook.Stop", "s1", nil))
	tracker.HandleEvent(context.Background(), event("agent.hook.SubagentStop", "s1", nil))
	if len(tracker.Busy()) != 0 || !received(changes) {
		t.Fatal("Stop ends the turn, and Claude Code's background subagents finishing after it don't restart it")
	}
	tracker.HandleEvent(context.Background(), event("agent.hook.PostToolUse", "s1", nil))
	if len(tracker.Busy()) != 1 {
		t.Fatal("a tool running after a blocked Stop means the turn went on")
	}
}

func TestTheTerminalEndsATurnNoHookReports(t *testing.T) {
	tracker := newTestTracker(&fakeSource{})
	changes, unsubscribe := tracker.SubscribeActivity()
	defer unsubscribe()
	programStatus := func(payload map[string]any) cmux.Event {
		return event("program_status.changed", "s1", payload)
	}

	tracker.HandleEvent(context.Background(), event("agent.hook.UserPromptSubmit", "s1", nil))
	received(changes)
	tracker.HandleEvent(context.Background(), programStatus(map[string]any{"event": "report", "record": map[string]any{"state": "working"}}))
	tracker.HandleEvent(context.Background(), programStatus(map[string]any{"event": "report", "record": map[string]any{"state": "blocked"}}))
	if len(tracker.Busy()) != 1 || received(changes) {
		t.Fatal("working and waiting on a permission prompt are still mid-turn")
	}
	tracker.HandleEvent(context.Background(), programStatus(map[string]any{"event": "report", "record": map[string]any{"state": "idle"}}))
	if len(tracker.Busy()) != 0 || !received(changes) {
		t.Fatal("an interrupt with Esc reports idle without a Stop hook")
	}
	tracker.HandleEvent(context.Background(), programStatus(map[string]any{"event": "report", "record": map[string]any{"state": "working"}}))
	if len(tracker.Busy()) != 0 {
		t.Fatal("other programs report progress too, so only hooks start a turn")
	}

	tracker.HandleEvent(context.Background(), event("agent.hook.UserPromptSubmit", "s1", nil))
	tracker.HandleEvent(context.Background(), programStatus(map[string]any{"event": "prompt_start", "record": nil}))
	if len(tracker.Busy()) != 0 {
		t.Fatal("the shell prompt coming back means a killed agent is gone")
	}

	tracker.HandleEvent(context.Background(), event("agent.hook.UserPromptSubmit", "s1", nil))
	tracker.HandleEvent(context.Background(), event("surface.closed", "s1", nil))
	if len(tracker.Busy()) != 0 {
		t.Fatal("a closed terminal has no agent")
	}
}
