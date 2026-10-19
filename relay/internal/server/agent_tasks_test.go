package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

func TestStartingAnAgentTaskRunsTheAgentOnThePromptInANewWorkspace(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.workspaceList = agentWorkspaces()
	relay.cmux.submitted = true
	folder := t.TempDir()
	ctx, conn := relay.connect(t)
	prompt := "Fix the flaky test.\nDon't touch `main`, and keep \"$HOME\" as is; it's fine."

	_ = wsjson.Write(ctx, conn, map[string]any{
		"type": "start_agent_task", "id": "1", "agent": "codex", "cwd": folder + "/", "prompt": "\n" + prompt + "\n", "title": " Flaky test ",
	})
	result := resultFor(t, ctx, conn, "1")
	if result["ok"] != true {
		t.Fatalf("start_agent_task failed: %v", result)
	}
	if data := result["data"].(map[string]any); data["workspace_id"] != "w2" || data["surface_id"] != "t2" {
		t.Fatalf("the answer must point at the new terminal: %v", data)
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	want := []envWorkspace{{title: "Flaky test", cwd: folder, env: map[string]string{"MUXER_TASK_PROMPT": prompt}}}
	if !reflect.DeepEqual(relay.cmux.envWorkspaces, want) {
		t.Fatalf("workspaces = %+v, want %+v", relay.cmux.envWorkspaces, want)
	}
	if commands := []string{`w2/t2 codex -- "$MUXER_TASK_PROMPT"`}; !reflect.DeepEqual(relay.cmux.commands, commands) {
		t.Fatalf("the prompt must reach the agent through the environment, not the typed command: %q", relay.cmux.commands)
	}
}

func TestAgentTasksExpandTheHomeFolder(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	relay := startRelay(t)
	relay.cmux.workspaceList = agentWorkspaces()
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "start_agent_task", "id": "1", "agent": "claude", "cwd": "~/app", "prompt": "hi"})
	if result := resultFor(t, ctx, conn, "1"); result["ok"] != true {
		t.Fatalf("start_agent_task failed: %v", result)
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if got := relay.cmux.envWorkspaces[0].cwd; got != filepath.Join(home, "app") {
		t.Fatalf("cwd = %q", got)
	}
	if !reflect.DeepEqual(relay.cmux.commands, []string{`w2/t2 claude -- "$MUXER_TASK_PROMPT"`}) || !reflect.DeepEqual(relay.cmux.keys, []string{"t2:enter"}) {
		t.Fatalf("commands = %q, keys = %q", relay.cmux.commands, relay.cmux.keys)
	}
}

func TestAgentTasksRefuseBadRequestsBeforeTouchingCmux(t *testing.T) {
	relay := startRelay(t)
	relay.server.options.InstalledAgents = func(context.Context) ([]string, error) { return []string{"codex"}, nil }
	folder := t.TempDir()
	ctx, conn := relay.connect(t)

	cases := []struct {
		request map[string]any
		code    string
	}{
		{map[string]any{"agent": "pi", "cwd": folder, "prompt": "hi"}, "invalid_params"},
		{map[string]any{"agent": "codex", "cwd": folder, "prompt": "  \n "}, "invalid_params"},
		{map[string]any{"agent": "codex", "cwd": folder, "prompt": strings.Repeat("a", maxTaskPromptBytes+1)}, "invalid_params"},
		{map[string]any{"agent": "codex", "cwd": "code/app", "prompt": "hi"}, "invalid_params"},
		{map[string]any{"agent": "codex", "cwd": filepath.Join(folder, "missing"), "prompt": "hi"}, "not_found"},
		{map[string]any{"agent": "claude", "cwd": folder, "prompt": "hi"}, "agent_missing"},
	}
	for index, test := range cases {
		id := string(rune('a' + index))
		test.request["type"] = "start_agent_task"
		test.request["id"] = id
		_ = wsjson.Write(ctx, conn, test.request)
		if result := resultFor(t, ctx, conn, id); result["ok"] != false || errorCode(result) != test.code {
			t.Fatalf("case %d: got %v, want %s", index, result, test.code)
		}
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if len(relay.cmux.envWorkspaces) != 0 {
		t.Fatalf("no workspace may open for a bad request: %+v", relay.cmux.envWorkspaces)
	}
}

func TestInstalledAgentsFallBackToEveryAgentWhenTheShellFails(t *testing.T) {
	relay := startRelay(t)
	found := []string{"claude"}
	var failure error
	relay.server.options.InstalledAgents = func(context.Context) ([]string, error) { return found, failure }
	ctx, conn := relay.connect(t)

	agents := func(id string) []any {
		_ = wsjson.Write(ctx, conn, map[string]any{"type": "installed_agents", "id": id})
		return resultFor(t, ctx, conn, id)["data"].(map[string]any)["agents"].([]any)
	}
	if got := agents("1"); !slices.Equal(got, []any{"claude"}) {
		t.Fatalf("agents = %v", got)
	}
	failure = errors.New("shell timed out")
	if got := agents("2"); !slices.Equal(got, []any{"claude", "codex"}) {
		t.Fatalf("an unknown answer must offer every agent: %v", got)
	}
}

func folderWorkspaces(folder string) *cmux.WorkspaceList {
	list := agentWorkspaces()
	list.Workspaces[0].CurrentDirectory = &folder
	return list
}

func TestAgentTasksCanStartInAnOpenWorkspace(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.workspaceList = folderWorkspaces("/src/api")
	relay.cmux.submitted = true
	ctx, conn := relay.connect(t)
	prompt := "Review `git diff`; keep \"$PATH\" and 'quotes' as they are."

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "start_agent_task", "id": "1", "agent": "claude", "workspace_id": "w1", "prompt": prompt})
	result := resultFor(t, ctx, conn, "1")
	if result["ok"] != true {
		t.Fatalf("start_agent_task failed: %v", result)
	}
	if data := result["data"].(map[string]any); data["workspace_id"] != "w1" || data["surface_id"] != "t5" {
		t.Fatalf("the answer must point at the new terminal in that workspace: %v", data)
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	want := []envTerminal{{workspaceID: "w1", cwd: "/src/api", env: map[string]string{"MUXER_TASK_PROMPT": prompt}}}
	if !reflect.DeepEqual(relay.cmux.envTerminals, want) {
		t.Fatalf("terminals = %+v, want %+v", relay.cmux.envTerminals, want)
	}
	if len(relay.cmux.envWorkspaces) != 0 {
		t.Fatalf("no workspace may open: %+v", relay.cmux.envWorkspaces)
	}
	if commands := []string{`w1/t5 claude -- "$MUXER_TASK_PROMPT"`}; !reflect.DeepEqual(relay.cmux.commands, commands) {
		t.Fatalf("the prompt must reach the agent through the environment, not the typed command: %q", relay.cmux.commands)
	}
}

func TestAgentTasksInAWorkspaceCanUseAnotherFolder(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.workspaceList = folderWorkspaces("/src/api")
	folder := t.TempDir()
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "start_agent_task", "id": "1", "agent": "codex", "workspace_id": "w1", "cwd": folder + "/", "prompt": "hi"})
	if result := resultFor(t, ctx, conn, "1"); result["ok"] != true {
		t.Fatalf("start_agent_task failed: %v", result)
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if len(relay.cmux.envTerminals) != 1 || relay.cmux.envTerminals[0].cwd != folder {
		t.Fatalf("terminals = %+v", relay.cmux.envTerminals)
	}
}

func TestAgentTasksInAWorkspaceRefuseWhatTheyCantServe(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.workspaceList = folderWorkspaces("/src/api")
	relay.cmux.remotes = map[string]cmux.RemoteState{"w2": {State: "connected", Destination: "box"}}
	ctx, conn := relay.connect(t)

	cases := []struct {
		request map[string]any
		code    string
	}{
		{map[string]any{"workspace_id": "gone"}, "not_found"},
		{map[string]any{"workspace_id": "w2"}, "invalid_params"},
		{map[string]any{"workspace_id": "w1", "cwd": filepath.Join(t.TempDir(), "missing")}, "not_found"},
	}
	for index, test := range cases {
		id := string(rune('a' + index))
		test.request["type"] = "start_agent_task"
		test.request["id"] = id
		test.request["agent"] = "claude"
		test.request["prompt"] = "hi"
		_ = wsjson.Write(ctx, conn, test.request)
		if result := resultFor(t, ctx, conn, id); result["ok"] != false || errorCode(result) != test.code {
			t.Fatalf("case %d: got %v, want %s", index, result, test.code)
		}
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if len(relay.cmux.envTerminals) != 0 || len(relay.cmux.envWorkspaces) != 0 {
		t.Fatalf("nothing may open: %+v %+v", relay.cmux.envTerminals, relay.cmux.envWorkspaces)
	}
}

func TestTheWelcomeTellsWhetherTasksCanStartInAWorkspace(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.workspaceList = folderWorkspaces("/src/api")
	relay.cmux.methods = []string{"system.identify", "mobile.workspace.list", cmux.MethodCreateWorkspace}
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	welcome := resultFor(t, ctx, conn, "1")["data"].(map[string]any)
	if features, _ := welcome["features"].([]any); !slices.Contains(features, any("start_agent_task.workspace_id")) {
		t.Fatalf("the welcome must say the relay understands workspace_id: %v", welcome)
	}
	unsupported, _ := welcome["unsupported"].([]any)
	if !slices.Contains(unsupported, any("start_agent_task.workspace_id")) || slices.Contains(unsupported, any("start_agent_task")) {
		t.Fatalf("without surface.create only tasks in a workspace are unsupported: %v", unsupported)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "start_agent_task", "id": "2", "agent": "claude", "workspace_id": "w1", "prompt": "hi"})
	if result := resultFor(t, ctx, conn, "2"); errorCode(result) != "unsupported" {
		t.Fatalf("got %v, want unsupported", result)
	}
}
