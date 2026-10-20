package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

func TestViewerFollowsARunThroughTheRelay(t *testing.T) {
	fake := newFakePostHog(
		cloudLine("b-1", "session/prompt", `{"prompt":[{"type":"text","text":"fix the login bug"}]}`),
		cloudLine("b-2", "session/update", `{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"Looking."}}}`),
	)
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)
	configPath := filepath.Join(t.TempDir(), "posthog-cloud.json")
	if err := posthogcloud.SaveConfig(configPath, posthogcloud.Config{Host: api.URL, ProjectID: 2, APIKey: "phx_test", UserID: 42}); err != nil {
		t.Fatal(err)
	}
	relay := startRelayWith(t, func(options *Options) {
		options.PostHogCloudConfig = configPath
		options.PostHogCloudHTTP = api.Client()
		options.PollInterval.PostHogCloud = time.Hour
		options.PostHogCloudFollower = posthogcloud.FollowerOptions{RetryBase: 10 * time.Millisecond}
	})
	socketDir, err := os.MkdirTemp("/tmp", "mx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "admin.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = ServeAdmin(ctx, socket, relay.store, AdminHooks{AttachPostHogCloud: relay.server.AttachPostHogCloud})
	}()
	waitUntil(t, "the admin socket", func() bool { _, err := os.Stat(socket); return err == nil })

	conn, err := DialAdmin(socket, AdminRequest{Command: "posthog_cloud_attach", TaskID: "t1", RunID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	frames := bufio.NewScanner(conn)
	frames.Buffer(make([]byte, 64<<10), 1<<20)
	next := func(what string, match func(posthogcloud.AttachFrame) bool) posthogcloud.AttachFrame {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for frames.Scan() {
			var frame posthogcloud.AttachFrame
			if json.Unmarshal(frames.Bytes(), &frame) == nil && match(frame) {
				return frame
			}
		}
		t.Fatalf("no %s: %v", what, frames.Err())
		return posthogcloud.AttachFrame{}
	}
	act := func(action posthogcloud.AttachAction) {
		t.Helper()
		encoded, _ := json.Marshal(action)
		if _, err := conn.Write(append(encoded, '\n')); err != nil {
			t.Fatal(err)
		}
	}

	snapshot := next("the history", func(frame posthogcloud.AttachFrame) bool {
		return frame.Type == posthogcloud.AttachSnapshot && frame.Ready
	})
	if len(snapshot.Entries) != 2 || snapshot.Entries[1].Text != "Looking." || snapshot.Status != "in_progress" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	act(posthogcloud.AttachAction{Action: posthogcloud.ActionSend, Text: "also check signup"})
	if result := next("the send result", func(frame posthogcloud.AttachFrame) bool { return frame.Type == posthogcloud.AttachResult }); !result.OK {
		t.Fatalf("send = %+v", result)
	}
	if sent := fake.sent("user_message"); len(sent) != 1 || sent[0].Params["content"] != "also check signup" {
		t.Fatalf("user_message = %+v", sent)
	}

	fake.events <- cloudEvent("s-1", `{"type":"permission_request","requestId":"q1","toolCall":{"toolCallId":"c1","title":"Run the tests","kind":"execute"},"options":[{"optionId":"yes","kind":"allow_once"},{"optionId":"no","kind":"reject_once"}]}`)
	waiting := next("the approval", func(frame posthogcloud.AttachFrame) bool { return len(frame.Pending) == 1 })
	if waiting.State != posthogcloud.StateWaiting || waiting.Pending[0].RequestID != "q1" {
		t.Fatalf("waiting = %+v", waiting)
	}
	act(posthogcloud.AttachAction{Action: posthogcloud.ActionAnswer, Mode: "deny"})
	if result := next("the answer result", func(frame posthogcloud.AttachFrame) bool { return frame.Type == posthogcloud.AttachResult }); !result.OK {
		t.Fatalf("answer = %+v", result)
	}
	if answer := fake.waitForCommand(t, "permission_response", 1)[0]; answer.Params["optionId"] != "no" {
		t.Fatalf("permission_response = %v", answer.Params)
	}
	act(posthogcloud.AttachAction{Action: posthogcloud.ActionAnswer, Mode: "once"})
	if result := next("the second answer", func(frame posthogcloud.AttachFrame) bool { return frame.Type == posthogcloud.AttachResult }); result.OK || result.Error == "" {
		t.Fatalf("an answered approval can't be answered again: %+v", result)
	}
	if count := fake.streamCount(); count != 1 {
		t.Fatalf("the viewer must ride the relay's stream, got %d streams", count)
	}
}
