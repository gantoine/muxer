package server

import (
	"encoding/json"
	"testing"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const topFixture = `{
  "sample": {"sampled_at": "2026-10-16T20:20:46Z"},
  "totals": {"cpu_percent": 30.5, "memory_bytes": 9000000000, "process_count": 31},
  "memory_diagnostic": {"app": {"physical_footprint_bytes": 700000000}},
  "coding_agents": [{"id": "claude", "display_name": "Claude Code", "resources": {"cpu_percent": 5.6, "memory_bytes": 1287558976, "process_count": 4}}],
  "windows": [{"workspaces": [
    {"id": "w1", "title": "Website", "resources": {"cpu_percent": 0, "memory_bytes": 10000000, "process_count": 3}, "panes": []},
    {"id": "w2", "title": "Muxer", "resources": {"cpu_percent": 24.2, "memory_bytes": 8500000000, "process_count": 9}, "panes": [
      {"surfaces": [{"processes": [
        {"pid": 52865, "name": "2.1.301", "path": "/Users/me/.local/share/claude/versions/2.1.301", "resources": {"cpu_percent": 8.8, "memory_bytes": 320000000}, "children": [
          {"pid": 7000, "name": "qemu-system-aar", "path": "/sdk/emulator/qemu-system-aarch64", "resources": {"cpu_percent": 12, "memory_bytes": 7330000000}, "children": []},
          {"pid": 7001, "name": "cmux", "path": "/Applications/cmux.app/Contents/Resources/bin/cmux", "resources": {"cpu_percent": 0, "memory_bytes": 0}, "children": []}
        ]}
      ]}]},
      {"surfaces": [{"processes": [
        {"pid": 7000, "name": "qemu-system-aarch64", "path": "/sdk/emulator/qemu-system-aarch64", "resources": {"cpu_percent": 12, "memory_bytes": 7330000000}, "children": []}
      ]}]}
    ]}
  ]}]
}`

func TestPhoneSeesWhatUsesTheComputer(t *testing.T) {
	relay := startRelay(t)
	if err := json.Unmarshal([]byte(topFixture), &relay.cmux.top); err != nil {
		t.Fatal(err)
	}
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "top", "id": "2"})
	result := resultFor(t, ctx, conn, "2")
	if result["ok"] != true {
		t.Fatalf("top must succeed: %v", result)
	}
	data := result["data"].(map[string]any)
	if data["process_count"] != float64(31) || data["app_memory_bytes"] != float64(700000000) {
		t.Fatalf("totals must come through: %v", data)
	}
	if agent := data["agents"].([]any)[0].(map[string]any); agent["name"] != "Claude Code" || agent["memory_bytes"] != float64(1287558976) {
		t.Fatalf("agents must be named: %v", agent)
	}
	workspaces := data["workspaces"].([]any)
	heaviest := workspaces[0].(map[string]any)
	if heaviest["title"] != "Muxer" {
		t.Fatalf("workspaces must be sorted by memory: %v", workspaces)
	}
	processes := heaviest["processes"].([]any)
	if len(processes) != 2 {
		t.Fatalf("a process seen in two terminals must be counted once, and idle ones left out: %v", processes)
	}
	if first := processes[0].(map[string]any); first["name"] != "qemu-system-aarch64" {
		t.Fatalf("processes must be sorted by memory and keep the name macOS cuts off: %v", processes)
	}
	if second := processes[1].(map[string]any); second["name"] != "claude" {
		t.Fatalf("a binary named after its version must take its folder's name: %v", second)
	}

	relay.cmux.mu.Lock()
	relay.cmux.topErr = &cmux.RPCError{Code: "method_not_found", Message: "Unknown method"}
	relay.cmux.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "top", "id": "3"})
	if result := resultFor(t, ctx, conn, "3"); result["ok"] != false || errorCode(result) != "unsupported" {
		t.Fatalf("an older cmux must be reported as unsupported: %v", result)
	}
}

func TestPhoneSeesHowBusyTheComputerIs(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "host_usage", "id": "2"})
	result := resultFor(t, ctx, conn, "2")
	data, _ := result["data"].(map[string]any)
	if result["ok"] != true || data["cpu_percent"] != 23.5 || data["memory_total_bytes"] != float64(48<<30) {
		t.Fatalf("host usage must come through: %v", result)
	}
}
