//go:build e2e

package e2e

import (
	"testing"
	"time"
)

func TestKeepAwakeCanBeToggledAndIsRestored(t *testing.T) {
	p := connect(t)
	original := flag(p.must("keep_awake", nil)["enabled"])
	t.Cleanup(func() { p.request("set_keep_awake", map[string]any{"enabled": original}) })

	changed := p.must("set_keep_awake", map[string]any{"enabled": !original})
	if flag(changed["enabled"]) == original {
		t.Fatalf("set_keep_awake didn't change anything: %v", changed)
	}
	p.eventually("keep_awake", "the keep-awake push", func(push message) bool {
		return flag(push["enabled"]) == !original
	})
}

func TestMetricsCoverCmuxAndTheComputer(t *testing.T) {
	p := connect(t)
	ws := scratchWorkspace(t, p)
	p.workspaceSatisfies(ws.ID, "the scratch workspace", func(message) bool { return true })

	var top message
	for range 15 {
		top = p.must("top", nil)
		if find(list(top["workspaces"]), "id", ws.ID) != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if find(list(top["workspaces"]), "id", ws.ID) == nil || number(top["process_count"]) == 0 {
		t.Fatalf("top doesn't describe the scratch workspace: %v", top)
	}

	usage := p.must("host_usage", nil)
	if number(usage["memory_total_bytes"]) <= 0 || number(usage["memory_used_bytes"]) <= 0 {
		t.Fatalf("host_usage answered %v", usage)
	}
}

func TestRecentSessionsAnswer(t *testing.T) {
	p := connect(t)
	sessions := p.must("recent_sessions", map[string]any{"limit": 5})
	if _, ok := sessions["sessions"].([]any); !ok {
		t.Fatalf("recent_sessions answered %v", sessions)
	}
}

func TestAgentTasksCheckTheFolderBeforeOpeningAWorkspace(t *testing.T) {
	p := connect(t)
	installed := p.must("installed_agents", nil)
	if _, ok := installed["agents"].([]any); !ok {
		t.Fatalf("installed_agents answered %v", installed)
	}
	failure := p.failure("start_agent_task", map[string]any{"agent": "claude", "cwd": t.TempDir() + "/missing", "prompt": "hi"})
	if failure.Code != "not_found" {
		t.Fatalf("a missing folder must be refused: %v", failure)
	}
}

func TestAgentTasksRefuseAnUnknownWorkspace(t *testing.T) {
	p := connect(t)
	failure := p.failure("start_agent_task", map[string]any{"agent": "claude", "workspace_id": "00000000-0000-0000-0000-000000000000", "prompt": "hi"})
	if failure.Code != "not_found" {
		t.Fatalf("an unknown workspace must be refused: %v", failure)
	}
}
