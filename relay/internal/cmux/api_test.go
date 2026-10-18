package cmux

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
)

func TestReplayFillsInTheTerminalAndRejectsAMissingScreen(t *testing.T) {
	fake := startFakeSocket(t, func(_ string, params map[string]any, _ bool) string {
		if params["surface_id"] == "blank" {
			return `{"ok":true,"result":{}}`
		}
		return `{"ok":true,"result":{"render_grid":{"columns":80,"rows":2}}}`
	})
	client := NewClient(fake.path, nil)
	defer client.Close()

	replay, err := client.Replay(context.Background(), "w1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if replay.WorkspaceID != "w1" || replay.SurfaceID != "t1" {
		t.Fatalf("the replay must name the terminal it came from: %+v", replay)
	}
	if _, err := client.History(context.Background(), "w1", "blank", 10); ErrorCode(err) != "no_render_grid" {
		t.Fatalf("a replay without a screen must be an error, got %v", err)
	}
}

func TestMarkSurfaceNotificationsReadMarksOnlyThatTerminalByID(t *testing.T) {
	var mu sync.Mutex
	var marked []string
	fake := startFakeSocket(t, func(method string, params map[string]any, _ bool) string {
		switch method {
		case MethodNotificationList:
			return `{"ok":true,"result":{"notifications":[
				{"id":"n1","surface_id":"t1"},
				{"id":"n2","surface_id":"t2"},
				{"id":"n3","surface_id":"t1","is_read":true},
				{"id":"gone","surface_id":"t1"}]}}`
		case MethodMarkNotification:
			if params["id"] == "gone" {
				return `{"ok":false,"error":{"code":"not_found","message":"gone"}}`
			}
			mu.Lock()
			marked = append(marked, params["id"].(string))
			mu.Unlock()
		}
		return `{"ok":true,"result":{}}`
	})
	client := NewClient(fake.path, nil)
	defer client.Close()

	if err := client.MarkSurfaceNotificationsRead(context.Background(), "t1"); err != nil {
		t.Fatalf("a notification that is already gone must not fail the call: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(marked, []string{"n1"}) {
		t.Fatalf("only the terminal's unread notifications must be marked, by id: %v", marked)
	}
}

func TestClientRereadsThePasswordWhenCmuxRejectsIt(t *testing.T) {
	fake := startFakeSocket(t, func(method string, _ map[string]any, authenticated bool) string {
		switch {
		case method == "auth.login" && !authenticated:
			return `{"ok":false,"error":{"code":"auth_failed","message":"bad"}}`
		case !authenticated:
			return `{"ok":false,"error":{"code":"auth_required","message":"login first"}}`
		default:
			return `{"ok":true,"result":{"authenticated":true,"version":"1"}}`
		}
	})
	var mu sync.Mutex
	password, reads := "old", 0
	client := NewClient(fake.path, func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		return password, nil
	})
	defer client.Close()
	if _, err := client.Identify(context.Background()); err == nil {
		t.Fatal("a wrong password must fail")
	}
	mu.Lock()
	password = "secret"
	mu.Unlock()
	if _, err := client.Identify(context.Background()); err != nil {
		t.Fatalf("a rotated password must be picked up: %v", err)
	}
	before := reads
	client.Close()
	if _, err := client.Identify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads != before {
		t.Fatalf("a working password must be reused, not read again: %d reads, then %d", before, reads)
	}
}

func TestEventTargetsPreferTheEventOverItsPayload(t *testing.T) {
	surface, workspace := "t-event", "w-event"
	payload, _ := json.Marshal(map[string]string{"surface_id": "t-payload", "workspace_id": "w-payload", "hook_event_name": "AskUserQuestion"})
	event := Event{Name: "agent.hook.PreToolUse", SurfaceID: &surface, WorkspaceID: &workspace, Payload: payload}
	if event.Surface() != "t-event" || event.Workspace() != "w-event" {
		t.Fatalf("the event's own ids win: %q %q", event.Surface(), event.Workspace())
	}
	if hook, ok := event.Hook(); !ok || hook != "AskUserQuestion" {
		t.Fatalf("the payload names the hook: %q %v", hook, ok)
	}

	bare := Event{Name: "agent.hook.Stop", Payload: json.RawMessage(`{"surface_id":"t-payload"}`)}
	if bare.Surface() != "t-payload" {
		t.Fatalf("the payload fills in missing ids: %q", bare.Surface())
	}
	if hook, ok := bare.Hook(); !ok || hook != "Stop" {
		t.Fatalf("the event name names the hook when the payload does not: %q", hook)
	}
	if _, ok := (Event{Name: "notification.created"}).Hook(); ok {
		t.Fatal("only agent.hook events are hooks")
	}
}

func TestAutomationCallsUseCmuxNamesAndShapes(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	fake := startFakeSocket(t, func(method string, params map[string]any, _ bool) string {
		id, _ := params["id"].(string)
		mu.Lock()
		calls = append(calls, method+" "+id)
		mu.Unlock()
		switch method {
		case MethodAutomationList:
			return `{"ok":true,"result":{"rules":[{"id":"r1","enabled":true,"event":null,"category":"agent","action_count":2,"rate_limit":{"interval_seconds":1,"maximum":1}}]}}`
		case MethodAutomationShow:
			return `{"ok":true,"result":{"id":"r1","when":{"category":"agent"},"then":[{"action":"notify","title":"Hi"}],"enabled":false}}`
		case MethodAutomationLogs:
			return `{"ok":true,"result":{"logs":[{"occurred_at":"2026-10-18T09:00:00Z","rule_id":"r1","event":"agent.needs_input","status":"completed","detail":"","chain":["r1"]}]}}`
		}
		return `{"ok":true,"result":{"id":"r1","enabled":false,"pending":true}}`
	})
	client := NewClient(fake.path, nil)
	defer client.Close()
	ctx := context.Background()

	rules, err := client.Automations(ctx)
	if err != nil || len(rules) != 1 || rules[0].Event != nil || *rules[0].Category != "agent" || rules[0].RateLimit.Maximum != 1 {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	rule, err := client.Automation(ctx, "r1")
	if err != nil || rule.Enabled == nil || *rule.Enabled || rule.Then[0]["title"] != "Hi" {
		t.Fatalf("rule = %+v, %v", rule, err)
	}
	logs, err := client.AutomationLogs(ctx, 10)
	if err != nil || len(logs) != 1 || logs[0].Status != "completed" {
		t.Fatalf("logs = %+v, %v", logs, err)
	}
	if err := client.SetAutomationEnabled(ctx, "r1", false); err != nil {
		t.Fatal(err)
	}
	if err := client.ReloadAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(calls, "automation.disable r1") || !slices.Contains(calls, "automation.reload ") {
		t.Fatalf("calls = %v", calls)
	}
}
