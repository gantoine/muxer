package server

import (
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/approvals"
	"github.com/gantoine/muxer/relay/internal/notify"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

func TestPhoneActsOnAPostHogCloudRun(t *testing.T) {
	fake := newFakePostHog(cloudLine("b-1", "session/prompt", `{"prompt":[{"type":"text","text":"fix the login bug"}]}`))
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)
	configPath := filepath.Join(t.TempDir(), "posthog-cloud.json")
	if err := posthogcloud.SaveConfig(configPath, posthogcloud.Config{Host: api.URL, ProjectID: 2, APIKey: "phx_test", UserID: 42, ClaudeToken: "sk-ant-oat-test"}); err != nil {
		t.Fatal(err)
	}
	relay := startRelayWithHub(t, func(options *Options) {
		options.PostHogCloudConfig = configPath
		options.PostHogCloudHTTP = api.Client()
		options.PollInterval.PostHogCloud = 20 * time.Millisecond
		options.PostHogCloudFollower = posthogcloud.FollowerOptions{RetryBase: 10 * time.Millisecond}
	}, notify.NewHub(nil, slog.New(slog.DiscardHandler)))
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "hello"})
	if features := resultFor(t, ctx, conn, "hello")["data"].(map[string]any)["features"].([]any); !containsAny(features, postHogCloudActionsFeature) {
		t.Fatalf("the welcome must offer PostHog Cloud actions: %v", features)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "watch_posthog_cloud_run", "id": "watch", "task_id": "t1", "run_id": "r1"})
	if result := resultFor(t, ctx, conn, "watch"); result["ok"] != true {
		t.Fatalf("watch = %v", result)
	}
	readUntil(t, ctx, conn, "posthog_cloud_chat")

	fake.events <- cloudEvent("s-1", `{"type":"permission_request","requestId":"q1","toolCall":{"toolCallId":"c1","title":"Run the tests","kind":"execute","rawInput":{"command":"npm test"},"_meta":{"claudeCode":{"toolName":"Bash"}}},"options":[{"optionId":"allow","kind":"allow_once","name":"Yes"},{"optionId":"always","kind":"allow_always","name":"Always"},{"optionId":"reject","kind":"reject_once","name":"No"}]}`)
	pending := readMatching(t, ctx, conn, "a cloud approval", func(message map[string]any) bool {
		list, _ := message["approvals"].([]any)
		return message["type"] == "approvals" && len(list) == 1
	})["approvals"].([]any)[0].(map[string]any)
	if pending["id"] != "phc:r1:q1" || pending["kind"] != "permission" || pending["tool"] != "Bash" || pending["detail"] != "npm test" || pending["posthog_run_id"] != "r1" || pending["posthog_task_id"] != "t1" {
		t.Fatalf("the approval must carry the run and the command: %v", pending)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "answer_approval", "id": "answer", "approval_id": "phc:r1:q1", "mode": "always"})
	if result := resultFor(t, ctx, conn, "answer"); result["ok"] != true {
		t.Fatalf("answer = %v", result)
	}
	answer := fake.waitForCommand(t, "permission_response", 1)[0]
	if answer.Params["requestId"] != "q1" || answer.Params["optionId"] != "always" {
		t.Fatalf("permission_response = %v", answer.Params)
	}
	readMatching(t, ctx, conn, "the cleared approval", func(message map[string]any) bool {
		list, ok := message["approvals"].([]any)
		return message["type"] == "approvals" && ok && len(list) == 0
	})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "answer_approval", "id": "again", "approval_id": "phc:r1:q1", "mode": "once"})
	if result := resultFor(t, ctx, conn, "again"); errorCode(result) != "not_pending" {
		t.Fatalf("an answered approval must not be answered twice: %v", result)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_send", "id": "steer", "task_id": "t1", "run_id": "r1", "text": "  also check the logout page  "})
	if result := resultFor(t, ctx, conn, "steer"); result["ok"] != true {
		t.Fatalf("send = %v", result)
	}
	steer := fake.waitForCommand(t, "user_message", 1)[0]
	if steer.Params["content"] != "also check the logout page" || steer.Params["steer"] != true {
		t.Fatalf("a message mid-turn must steer: %v", steer.Params)
	}

	fake.mu.Lock()
	fake.notReady["cancel"] = 1
	fake.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_stop", "id": "stop", "task_id": "t1", "run_id": "r1"})
	if result := resultFor(t, ctx, conn, "stop"); result["ok"] != true {
		t.Fatalf("stop = %v", result)
	}
	if count := len(fake.sent("cancel")); count != 2 {
		t.Fatalf("a sandbox that isn't ready must be retried, got %d cancels", count)
	}

	fake.events <- cloudEvent("s-2", `{"type":"credential_request","requestId":"cr1","credential":"claude_subscription_token","expiresAt":"`+time.Now().Add(time.Minute).UTC().Format(time.RFC3339)+`"}`)
	token := fake.waitForCommand(t, "credential_response", 1)[0]
	if token.Params["requestId"] != "cr1" || token.Params["token"] != "sk-ant-oat-test" || token.Params["credential"] != "claude_subscription_token" {
		t.Fatalf("credential_response = %v", token.Params)
	}
	fake.events <- cloudEvent("s-3", `{"type":"credential_request","requestId":"cr1","credential":"claude_subscription_token"}`)

	fake.events <- cloudEvent("s-4", cloudLine("b-5", "session/update", `{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"All tests pass."}}}`))
	fake.events <- cloudEvent("s-5", cloudLine("b-6", "_posthog/turn_complete", `{"stopReason":"end_turn"}`))
	notice := readMatching(t, ctx, conn, "the turn notification", func(message map[string]any) bool {
		notification, _ := message["notification"].(map[string]any)
		return message["type"] == "notification" && notification["posthog_run_id"] == "r1"
	})["notification"].(map[string]any)
	if notice["title"] != "Fix login" || !strings.Contains(notice["subtitle"].(string), "Finished its turn") || notice["body"] != "All tests pass." || notice["posthog_task_id"] != "t1" {
		t.Fatalf("notification = %v", notice)
	}
	if count := len(fake.sent("credential_response")); count != 1 {
		t.Fatalf("a repeated credential request must not be answered twice, got %d", count)
	}

	fake.set("completed")
	fake.events <- cloudEvent("s-6", `{"type":"task_run_state","run_id":"r1","status":"completed"}`)
	waitForRun(t, ctx, conn, func(run map[string]any) bool { return run["status"] == "completed" })
	fake.mu.Lock()
	fake.notReady["user_message"] = 1
	fake.mu.Unlock()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "posthog_cloud_send", "id": "resume", "task_id": "t1", "run_id": "r1", "text": "one more thing"})
	if result := resultFor(t, ctx, conn, "resume"); result["ok"] != true {
		t.Fatalf("a reply to a finished run must bring it back: %v", result)
	}
	messages := fake.sent("user_message")
	if len(messages) != 3 || messages[2].Params["content"] != "one more thing" || messages[2].Params["steer"] != nil {
		t.Fatalf("user_message after resume = %+v", messages)
	}
	fake.mu.Lock()
	resumed := fake.resumed
	fake.mu.Unlock()
	if resumed != 1 {
		t.Fatalf("resume_in_cloud calls = %d", resumed)
	}
	waitForRun(t, ctx, conn, func(run map[string]any) bool { return run["status"] == "in_progress" })
	if count := fake.streamCount(); count < 2 {
		t.Fatalf("a resumed run must be followed again, got %d streams", count)
	}
}

func TestCloudAnswersMapOntoTheAgentsOptions(t *testing.T) {
	permission := posthogcloud.PendingApproval{RequestID: "q", Kind: posthogcloud.ApprovalPermission, Options: []posthogcloud.PermissionOption{
		{OptionID: "yes", Kind: "allow_once"}, {OptionID: "no", Kind: "reject_once"},
	}}
	params, err := permissionResponse(permission, approvals.Answer{Mode: "always"})
	if err != nil || params["optionId"] != "yes" {
		t.Fatalf("always without an always option must fall back to once: %v %v", params, err)
	}
	if _, err := permissionResponse(permission, approvals.Answer{Mode: "sometimes"}); !errors.As(err, new(approvals.InvalidAnswer)) {
		t.Fatalf("an unknown mode must be refused: %v", err)
	}

	question := posthogcloud.PendingApproval{RequestID: "q", Kind: posthogcloud.ApprovalQuestion, Questions: []posthogcloud.QuestionItem{
		{Question: "Which database?", Options: []posthogcloud.QuestionOption{{Label: "Postgres"}, {Label: "SQLite"}}},
		{Question: "Add tests?", Options: []posthogcloud.QuestionOption{{Label: "Yes"}, {Label: "No"}}},
	}}
	params, err = permissionResponse(question, approvals.Answer{Answers: []string{"SQLite", "Yes"}})
	answers, _ := params["answers"].(map[string]string)
	if err != nil || params["optionId"] != "option_1" || answers["Which database?"] != "SQLite" || answers["Add tests?"] != "Yes" {
		t.Fatalf("question = %v %v", params, err)
	}

	plan := posthogcloud.PendingApproval{RequestID: "q", Kind: posthogcloud.ApprovalPlan, Options: []posthogcloud.PermissionOption{
		{OptionID: "auto", Kind: "allow_always"}, {OptionID: "acceptEdits", Kind: "allow_always"}, {OptionID: "default", Kind: "allow_once"}, {OptionID: "reject_with_feedback", Kind: "reject_once"},
	}}
	params, _ = permissionResponse(plan, approvals.Answer{Mode: "autoAccept"})
	if params["optionId"] != "acceptEdits" {
		t.Fatalf("autoAccept = %v", params)
	}
	params, _ = permissionResponse(plan, approvals.Answer{Mode: "deny", Feedback: " Split it in two "})
	if params["optionId"] != "reject_with_feedback" || params["customInput"] != "Split it in two" {
		t.Fatalf("deny = %v", params)
	}
}
