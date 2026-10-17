package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

func TestPhoneCanSendImagesToClaude(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	resultFor(t, ctx, conn, "1")

	photo := base64.StdEncoding.EncodeToString([]byte("jpeg bytes"))
	images := []map[string]string{{"data_b64": photo, "format": "jpeg"}}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "send_images", "id": "2", "images": images})
	if result := resultFor(t, ctx, conn, "2"); errorCode(result) != "not_attached" {
		t.Fatalf("images need an attached terminal: %v", result)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "send_images", "id": "3", "images": images})
	if result := resultFor(t, ctx, conn, "3"); errorCode(result) != "no_agent" {
		t.Fatalf("a terminal without Claude must refuse images: %v", result)
	}

	relay.cmux.setAgent(cmux.AgentSession{Kind: "claude", SessionID: "s1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "send_images", "id": "4", "text": "what's wrong here?", "images": images})
	if result := resultFor(t, ctx, conn, "4"); result["ok"] != true {
		t.Fatalf("send_images failed: %v", result)
	}
	relay.cmux.mu.Lock()
	chats := relay.cmux.chats
	relay.cmux.mu.Unlock()
	if len(chats) != 1 || chats[0].sessionID != "s1" || chats[0].text != "what's wrong here?" || chats[0].images[0].Data != photo || chats[0].images[0].Format != "jpeg" {
		t.Fatalf("the images must reach the Claude session: %+v", chats)
	}

	for id, bad := range map[string][]map[string]string{
		"5": nil,
		"6": {{"data_b64": photo, "format": "bmp"}},
		"7": {{"data_b64": "not base64!", "format": "png"}},
		"8": {{"data_b64": base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes+1)), "format": "png"}},
		"9": {images[0], images[0], images[0], images[0], images[0]},
	} {
		_ = wsjson.Write(ctx, conn, map[string]any{"type": "send_images", "id": id, "images": bad})
		if result := resultFor(t, ctx, conn, id); errorCode(result) != "invalid_params" {
			t.Fatalf("request %s must be refused: %v", id, result)
		}
	}

	relay.cmux.mu.Lock()
	relay.cmux.chatErr = &cmux.RPCError{Code: "method_not_found", Message: "Unknown method"}
	relay.cmux.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "send_images", "id": "10", "images": images})
	if result := resultFor(t, ctx, conn, "10"); errorCode(result) != "unsupported" {
		t.Fatalf("an older cmux must be reported as unsupported: %v", result)
	}
}

func TestComputerNameComesFromCmuxUnlessSetByHand(t *testing.T) {
	relay := startRelay(t)
	relay.server.options.MacName = ""
	relay.server.options.FallbackMacName = "macbook"

	name := func() string {
		response, err := http.Get(relay.url + "/v1/health")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var health struct {
			MacName string `json:"mac_name"`
		}
		_ = json.NewDecoder(response.Body).Decode(&health)
		return health.MacName
	}

	relay.cmux.mu.Lock()
	relay.cmux.hostName = "Studio"
	relay.cmux.mu.Unlock()
	if got := name(); got != "Studio" {
		t.Fatalf("name = %q, want cmux's name", got)
	}

	relay.cmux.mu.Lock()
	relay.cmux.hostNameErr = &cmux.RPCError{Code: "method_not_found", Message: "Unknown method"}
	relay.cmux.mu.Unlock()
	if got := name(); got != "macbook" {
		t.Fatalf("name = %q, want the fallback when cmux can't say", got)
	}

	relay.server.options.MacName = "Work laptop"
	if got := name(); got != "Work laptop" {
		t.Fatalf("name = %q, want the name given with --name", got)
	}
}

func TestPhoneCanFetchImagesFromTheChat(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	resultFor(t, ctx, conn, "1")

	dir := t.TempDir()
	path := filepath.Join(dir, "clipboard-2026-10-17-084701-6FFF738B.jpeg")
	if err := os.WriteFile(path, []byte("jpeg bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "chat_image", "id": "2", "path": path})
	result := resultFor(t, ctx, conn, "2")
	data, _ := result["data"].(map[string]any)
	if result["ok"] != true || data["format"] != "jpeg" || data["data_b64"] != base64.StdEncoding.EncodeToString([]byte("jpeg bytes")) {
		t.Fatalf("chat_image = %v", result)
	}

	secret := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(secret, []byte("private"), 0o600)
	for id, other := range map[string]string{
		"3": secret,
		"4": dir + "/x/../clipboard-2026-10-17-084701-6FFF738B.jpeg",
		"5": filepath.Join(dir, "clipboard-2026-10-17-084701-00000000.jpeg"),
	} {
		_ = wsjson.Write(ctx, conn, map[string]any{"type": "chat_image", "id": id, "path": other})
		if result := resultFor(t, ctx, conn, id); result["ok"] == true {
			t.Errorf("chat_image served %s: %v", other, result)
		}
	}
}
