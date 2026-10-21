package posthogcloud

import (
	"strings"
	"testing"

	"github.com/gantoine/muxer/phcloud/internal/chat"
)

func feedAll(t *Transcript, lines ...string) []chat.Entry {
	var changed []chat.Entry
	for _, raw := range lines {
		changed = append(changed, t.Feed([]byte(raw))...)
	}
	return changed
}

func notification(eventID, method, params string) string {
	return `{"type":"notification","timestamp":"T","event_id":"` + eventID + `","notification":{"jsonrpc":"2.0","method":"` + method + `","params":` + params + `}}`
}

func update(eventID, body string) string {
	return notification(eventID, "session/update", `{"sessionId":"s1","update":`+body+`}`)
}

func TestPromptsAndRepliesBecomeEntries(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		notification("1", "_posthog/progress", `{"step":"sandbox","status":"in_progress","label":"Setting up sandbox"}`),
		notification("2", "session/prompt", `{"sessionId":"s1","prompt":[{"type":"text","text":"what is the last commit?"}]}`),
		update("3", `{"sessionUpdate":"agent_message","content":{"type":"text","text":"It is abc123."}}`),
		update("4", `{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"what is the last commit?"}}`),
		notification("5", "_posthog/turn_complete", `{"sessionId":"s1","stopReason":"end_turn"}`),
	)
	entries := transcript.Entries()
	if len(entries) != 2 {
		t.Fatalf("the echoed prompt must not repeat the user message: %+v", entries)
	}
	if entries[0].Kind != chat.KindUser || entries[0].Text != "what is the last commit?" || entries[0].ID != "2" {
		t.Errorf("user = %+v", entries[0])
	}
	if entries[1].Kind != chat.KindAssistant || entries[1].Text != "It is abc123." {
		t.Errorf("assistant = %+v", entries[1])
	}
	if transcript.State() != StateIdle {
		t.Errorf("a finished turn must leave the run idle, got %q", transcript.State())
	}
}

func TestChunksMergeUntilSomethingElseArrives(t *testing.T) {
	transcript := NewTranscript()
	changed := feedAll(transcript,
		notification("1", "session/prompt", `{"prompt":[{"type":"text","text":"go"}]}`),
		update("2", `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"**Planning**\n\nI"}}`),
		update("3", `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":" need a list"}}`),
		update("4", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`),
		update("5", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":" there"}}`),
		update("6", `{"sessionUpdate":"tool_call","toolCallId":"c1","title":"ls","kind":"execute","status":"pending","rawInput":{"command":"ls"}}`),
		update("7", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done"}}`),
	)
	if transcript.State() != StateWorking {
		t.Errorf("a turn in progress must read as working, got %q", transcript.State())
	}
	entries := transcript.Entries()
	if len(entries) != 5 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[1].Kind != chat.KindThinking || entries[1].Text != "**Planning**\n\nI need a list" || entries[1].ID != "2" {
		t.Errorf("thinking = %+v", entries[1])
	}
	if entries[2].Kind != chat.KindAssistant || entries[2].Text != "Hello there" || entries[2].ID != "4" {
		t.Errorf("assistant = %+v", entries[2])
	}
	if entries[4].Text != "Done" || entries[4].ID != "7" {
		t.Errorf("a message after a tool call must start a new entry: %+v", entries[4])
	}
	if last := changed[len(changed)-1]; last.ID != "7" {
		t.Errorf("the last change must be the new message: %+v", last)
	}
}

func TestToolCallsAreUpdatedInPlace(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		update("1", `{"_meta":{"claudeCode":{"toolName":"Bash"}},"toolCallId":"toolu_1","sessionUpdate":"tool_call","status":"pending","title":"List files","kind":"execute","rawInput":{"command":"ls -la\necho done","description":"List files"}}`),
		update("2", `{"_meta":{"claudeCode":{"toolName":"Bash"}},"toolCallId":"toolu_1","sessionUpdate":"tool_call_update","status":"completed","content":[{"type":"content","content":{"type":"text","text":"a.txt\n"}}]}`),
		update("3", `{"_meta":{"claudeCode":{"toolName":"Edit"}},"toolCallId":"toolu_2","sessionUpdate":"tool_call","status":"pending","title":"Edit notes.txt","kind":"edit","rawInput":{"file_path":"/repo/src/notes.txt"}}`),
		update("4", `{"toolCallId":"toolu_2","sessionUpdate":"tool_call_update","status":"failed","rawOutput":"String not found","content":[{"type":"diff","path":"/repo/src/notes.txt","oldText":"world\n","newText":"there\n"}]}`),
		update("5", `{"sessionUpdate":"tool_call","toolCallId":"exec-1","title":"posthog/exec","kind":"other","status":"in_progress","rawInput":{"command":"search ^skill-get$"},"_meta":{"posthog":{"toolName":"mcp__posthog__exec","mcp":{"server":"posthog","tool":"exec"}}}}`),
		update("6", `{"sessionUpdate":"tool_call_update","toolCallId":"exec-1","status":"completed","rawOutput":{"content":[{"type":"text","text":"[\"skill-get\"]"}]}}`),
		update("7", `{"sessionUpdate":"tool_call","toolCallId":"exec-2","title":"/bin/bash -lc 'cat README.md'","kind":"execute","status":"in_progress"}`),
	)
	entries := transcript.Entries()
	if len(entries) != 4 {
		t.Fatalf("each tool call must be one entry: %+v", entries)
	}
	bash := entries[0].Tool
	if entries[0].ID != "toolu_1" || bash.Name != "Bash" || bash.Summary != "ls -la" || bash.Status != chat.ToolDone || bash.Output != "a.txt" {
		t.Errorf("bash = %+v", bash)
	}
	edit := entries[1].Tool
	if edit.Name != "Edit" || edit.Summary != "src/notes.txt" || edit.Status != chat.ToolFailed {
		t.Errorf("edit = %+v", edit)
	}
	if len(edit.Diff) != 1 || strings.Join(edit.Diff[0].Lines, "|") != "-world|+there" {
		t.Errorf("edit diff = %+v", edit.Diff)
	}
	mcp := entries[2].Tool
	if mcp.Name != "posthog/exec" || mcp.Summary != "search ^skill-get$" || mcp.Output != `["skill-get"]` {
		t.Errorf("mcp = %+v", mcp)
	}
	if shell := entries[3].Tool; shell.Name != "Bash" || shell.Summary != "cat README.md" {
		t.Errorf("a shell call without metadata must read as Bash: %+v", shell)
	}
}

func TestApprovalsTrackWhetherTheRunWaits(t *testing.T) {
	request := `{"requestId":"r1","toolCallId":"c1","options":[{"optionId":"accept","kind":"allow_once","name":"Accept"},{"optionId":"decline","kind":"reject_once","name":"Decline"}],"toolCall":{"toolCallId":"c1","title":"Run rm -rf build"}}`
	transcript := NewTranscript()
	feedAll(transcript,
		notification("1", "session/prompt", `{"prompt":[{"type":"text","text":"clean up"}]}`),
		notification("2", "session/request_permission", request),
		notification("3", "_posthog/permission_request", request),
	)
	if transcript.State() != StateWaiting {
		t.Fatalf("a pending approval must read as waiting, got %q", transcript.State())
	}
	entries := transcript.Entries()
	if len(entries) != 2 || entries[1].Kind != chat.KindNotice || entries[1].Text != "Waiting for approval: Run rm -rf build" {
		t.Fatalf("the request must show once: %+v", entries)
	}
	changed := feedAll(transcript, notification("4", "_posthog/permission_resolved", `{"requestId":"r1","toolCallId":"c1","optionId":"decline"}`))
	if len(changed) != 1 || changed[0].ID != "approval:c1" || changed[0].Text != "Declined: Run rm -rf build" {
		t.Fatalf("the answer must update the same entry: %+v", changed)
	}
	if transcript.State() != StateWorking {
		t.Errorf("an answered approval must leave the run working, got %q", transcript.State())
	}
}

func TestATurnEndSettlesUnansweredApprovals(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		notification("1", "session/request_permission", `{"options":[{"optionId":"accept","kind":"allow_once"}],"toolCall":{"toolCallId":"c1","title":"Save summary"}}`),
		notification("2", "_posthog/turn_complete", `{"stopReason":"cancelled"}`),
	)
	entries := transcript.Entries()
	if len(entries) != 2 || entries[0].Text != "Asked for approval: Save summary" || entries[1].Text != "Interrupted" {
		t.Fatalf("entries = %+v", entries)
	}
	if transcript.State() != StateIdle {
		t.Errorf("state = %q", transcript.State())
	}
}

func TestPlansAndErrorsShow(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		notification("", "session/prompt", `{"prompt":[{"type":"text","text":"build it"}]}`),
		update("", `{"sessionUpdate":"plan","entries":[{"content":"Write code","status":"completed","priority":"high"},{"content":"Test it","status":"in_progress","priority":"high"}]}`),
		update("", `{"sessionUpdate":"plan","entries":[{"content":"Write code","status":"completed"},{"content":"Test it","status":"completed"}]}`),
		notification("", "_posthog/error", `{"source":"agent_server","message":"Sandbox ran out of memory"}`),
	)
	entries := transcript.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].ID != "e1" {
		t.Errorf("an entry without an event id must get a stable one: %+v", entries[0])
	}
	plan := entries[1].Tool
	if plan == nil || len(plan.Todos) != 2 || plan.Todos[1].Status != "completed" {
		t.Errorf("a later plan must replace the turn's plan: %+v", entries[1])
	}
	if entries[2].Kind != chat.KindNotice || entries[2].Text != "Sandbox ran out of memory" {
		t.Errorf("error = %+v", entries[2])
	}
}
