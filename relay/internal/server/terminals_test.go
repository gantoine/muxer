package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

func TestPhoneCanCloseRenameAndRestartATerminal(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "rename_terminal", "id": "1", "workspace_id": "w1"})
	if result := resultFor(t, ctx, conn, "1"); result["ok"] != false || errorCode(result) != "invalid_params" {
		t.Fatalf("a request without surface_id must be refused: %v", result)
	}

	requests := []map[string]any{
		{"type": "rename_terminal", "id": "2", "workspace_id": "w1", "surface_id": "t1", "title": "  server  "},
		{"type": "rename_terminal", "id": "3", "workspace_id": "w1", "surface_id": "t1", "title": " "},
		{"type": "restart_terminal", "id": "4", "workspace_id": "w1", "surface_id": "t1"},
		{"type": "close_terminal", "id": "5", "workspace_id": "w1", "surface_id": "t2", "force": true},
	}
	for _, request := range requests {
		_ = wsjson.Write(ctx, conn, request)
		if result := resultFor(t, ctx, conn, request["id"].(string)); result["ok"] != true {
			t.Fatalf("%s failed: %v", request["type"], result)
		}
	}
	relay.cmux.mu.Lock()
	calls := append([]string(nil), relay.cmux.terminalCalls...)
	relay.cmux.mu.Unlock()
	want := []string{
		`rename w1/t1 "server"`,
		`rename w1/t1 ""`,
		"restart w1/t1 /opt/homebrew/bin/fish",
		"close w1/t2 force=true",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

func TestCreatingATerminalAnswersWithItsSurfaceSoThePhoneCanOpenIt(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "create_terminal", "id": "1", "workspace_id": "w1"})
	result := resultFor(t, ctx, conn, "1")
	data, _ := result["data"].(map[string]any)
	if result["ok"] != true || data["surface_id"] != "t3" {
		t.Fatalf("create_terminal must answer with the new surface_id: %v", result)
	}
}

func TestClosingTheLastTerminalExplainsWhatToDoInstead(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.closeErr = &cmux.RPCError{Code: "invalid_state", Message: "Cannot close the last surface"}
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "close_terminal", "id": "1", "workspace_id": "w1", "surface_id": "t1"})
	if result := resultFor(t, ctx, conn, "1"); errorCode(result) != "last_terminal" {
		t.Fatalf("the last terminal must be reported as such: %v", result)
	}

	relay.cmux.mu.Lock()
	relay.cmux.closeErr = &cmux.RPCError{Code: "confirmation_required", Message: "Surface has a running process; retry with force=true"}
	relay.cmux.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "close_terminal", "id": "2", "workspace_id": "w1", "surface_id": "t1"})
	if result := resultFor(t, ctx, conn, "2"); errorCode(result) != "confirmation_required" {
		t.Fatalf("a busy terminal must ask the phone to confirm: %v", result)
	}

	relay.cmux.mu.Lock()
	relay.cmux.methods = []string{"system.identify"}
	relay.cmux.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "3"})
	resultFor(t, ctx, conn, "3")
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "restart_terminal", "id": "4", "workspace_id": "w1", "surface_id": "t1"})
	if result := resultFor(t, ctx, conn, "4"); errorCode(result) != "unsupported" {
		t.Fatalf("an older cmux must be reported as unsupported: %v", result)
	}
}

func TestAttachedTerminalReportsWhetherTheAgentIsWaitingOnYou(t *testing.T) {
	relay := startRelay(t)
	relay.server.options.PollInterval.InputState = 10 * time.Millisecond
	relay.cmux.inputState = cmux.InputState{SurfaceID: "t1", Agent: true, State: "empty"}
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	if state := readUntil(t, ctx, conn, "input_state"); state["surface_id"] != "t1" || state["waiting_on_human"] != false {
		t.Fatalf("the first state must be sent on attach: %v", state)
	}

	relay.cmux.mu.Lock()
	relay.cmux.inputState = cmux.InputState{SurfaceID: "t1", Agent: true, Lifecycle: "needsInput", State: "dialog", BlocksTyping: true, WaitingOnHuman: true}
	relay.cmux.mu.Unlock()
	state := readUntil(t, ctx, conn, "input_state")
	if state["waiting_on_human"] != true || state["blocks_typing"] != true || state["state"] != "dialog" {
		t.Fatalf("a prompt opening must be pushed: %v", state)
	}
}
