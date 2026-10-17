package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

func TestPhoneCanSearchFilesInClaudesFolder(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	resultFor(t, ctx, conn, "1")

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "chat_files", "id": "2", "query": "main"})
	if result := resultFor(t, ctx, conn, "2"); errorCode(result) != "not_attached" {
		t.Fatalf("file search needs an attached terminal: %v", result)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "chat_files", "id": "3", "query": "main"})
	if result := resultFor(t, ctx, conn, "3"); errorCode(result) != "no_agent" {
		t.Fatalf("a terminal without Claude has no folder to search: %v", result)
	}

	folder := t.TempDir()
	for _, name := range []string{"main.go", "cmd/tool/main.go", "README.md"} {
		path := filepath.Join(folder, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	relay.cmux.setAgent(cmux.AgentSession{Kind: "claude", SessionID: "s1", Cwd: folder})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "chat_files", "id": "4", "query": "main"})
	result := resultFor(t, ctx, conn, "4")
	data, _ := result["data"].(map[string]any)
	found, _ := data["files"].([]any)
	if len(found) != 2 || found[0] != "main.go" || found[1] != "cmd/tool/main.go" {
		t.Fatalf("chat_files = %v", result)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "chat_files", "id": "5", "query": string(make([]byte, maxFileQueryBytes+1))})
	if result := resultFor(t, ctx, conn, "5"); errorCode(result) != "invalid_params" {
		t.Fatalf("a huge query must be refused: %v", result)
	}
}
