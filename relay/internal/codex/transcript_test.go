package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gantoine/muxer/relay/internal/chat"
)

func feedAll(t *Transcript, lines ...string) []chat.Entry {
	var changed []chat.Entry
	for _, raw := range lines {
		changed = append(changed, t.Feed([]byte(raw))...)
	}
	return changed
}

func TestTypedItemsBecomeEntries(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"timestamp":"T0","type":"session_meta","payload":{"id":"thread"}}`,
		`{"timestamp":"T0","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>"}]}}`,
		`{"timestamp":"T1","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"local_image","path":"/var/T/clipboard-2026-10-17-201902-49EBBFDE.jpeg"},{"type":"text","text":"[Image #1] What colour?","text_elements":[{"placeholder":"[Image #1]"}]}]}}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"r1","summary_text":[]}}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"r2","summary_text":["**Looking at it**"]}}}`,
		`{"timestamp":"T3","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"Black"}],"phase":"final_answer"}}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	user := entries[0]
	if user.Kind != chat.KindUser || user.Text != "What colour?" || len(user.Images) != 1 || user.Time != "T1" {
		t.Errorf("user = %+v", user)
	}
	if entries[1].Kind != chat.KindThinking || entries[1].Text != "**Looking at it**" {
		t.Errorf("thinking = %+v", entries[1])
	}
	if entries[2].Kind != chat.KindAssistant || entries[2].Text != "Black" || entries[2].ID != "a1" {
		t.Errorf("assistant = %+v", entries[2])
	}
}

func TestCommandsAndFileChangesBecomeTools(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"timestamp":"T1","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"c1","command":["/bin/zsh","-lc","touch a.txt\necho done"],"status":"completed","aggregated_output":"done\n","exit_code":0}}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"c2","command":["/bin/zsh","-lc","false"],"status":"completed","aggregated_output":"","exit_code":1}}}`,
		`{"timestamp":"T3","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"f1","status":"completed","changes":{"/src/b.go":{"type":"add","unified_diff":"@@ -0,0 +1,2 @@\n+one\n+two\n"},"/src/a.go":{"type":"update","unified_diff":"--- a\n+++ b\n@@ -3,2 +3,2 @@\n-old\n+new\n context\n\\ No newline at end of file\n"}}}}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 4 {
		t.Fatalf("entries = %+v", entries)
	}
	shell := entries[0].Tool
	if shell.Name != "Shell" || shell.Summary != "touch a.txt" || shell.Status != chat.ToolDone || shell.Output != "done" {
		t.Errorf("shell = %+v", shell)
	}
	if entries[1].Tool.Status != chat.ToolFailed {
		t.Errorf("a non-zero exit must fail the tool, got %+v", entries[1].Tool)
	}
	edit, write := entries[2].Tool, entries[3].Tool
	if edit.Name != "Edit" || edit.Summary != "src/a.go" || len(edit.Diff) != 1 {
		t.Fatalf("edit = %+v", edit)
	}
	if hunk := edit.Diff[0]; hunk.OldStart != 3 || hunk.NewStart != 3 || len(hunk.Lines) != 3 || hunk.Lines[0] != "-old" || hunk.Lines[2] != " context" {
		t.Errorf("edit hunk = %+v", hunk)
	}
	if write.Name != "Write" || write.Summary != "src/b.go" || write.Diff[0].NewStart != 1 || len(write.Diff[0].Lines) != 2 {
		t.Errorf("write = %+v", write)
	}
}

func TestOlderRolloutsUseEventsAndCalls(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"timestamp":"T1","type":"event_msg","payload":{"type":"user_message","message":"why?\n","local_images":[]}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"agent_reasoning","text":"**Checking**"}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"agent_message","message":"Looking.","phase":"commentary"}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"agent_message","message":"Still looking.","phase":"commentary"}}`,
		`{"timestamp":"T3","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"pnpm -v\"}","call_id":"call_1"}}`,
		`{"timestamp":"T3","type":"response_item","payload":{"type":"function_call","name":"write_stdin","arguments":"{}","call_id":"call_2"}}`,
		`{"timestamp":"T4","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Chunk ID: 1\nProcess exited with code 127\nOutput:\nzsh:1: command not found: pnpm\n"}}`,
		`{"timestamp":"T4","type":"response_item","payload":{"type":"function_call_output","call_id":"call_2","output":"ignored"}}`,
		`{"timestamp":"T5","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /src/app/main.go\n@@\n-a\n+b\n*** End Patch\n","call_id":"call_3"}}`,
		`{"timestamp":"T6","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_3","output":"{\"output\":\"Success.\\n\",\"metadata\":{\"exit_code\":0}}"}}`,
		`{"timestamp":"T7","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"Read\",\"status\":\"completed\"},{\"step\":\"Fix\",\"status\":\"in_progress\"}]}","call_id":"call_4"}}`,
		`{"timestamp":"T8","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn_1","reason":"interrupted"}}`,
	)
	entries := transcript.Entries()
	kinds := []string{chat.KindUser, chat.KindThinking, chat.KindAssistant, chat.KindAssistant, chat.KindTool, chat.KindTool, chat.KindTool, chat.KindNotice}
	if len(entries) != len(kinds) {
		t.Fatalf("entries = %+v", entries)
	}
	for i, kind := range kinds {
		if entries[i].Kind != kind {
			t.Errorf("entry %d kind = %q, want %q", i, entries[i].Kind, kind)
		}
	}
	if entries[2].ID == entries[3].ID {
		t.Error("two messages at the same time need different ids")
	}
	shell := entries[4].Tool
	if shell.Summary != "pnpm -v" || shell.Status != chat.ToolFailed || shell.Output != "zsh:1: command not found: pnpm" {
		t.Errorf("shell = %+v", shell)
	}
	patch := entries[5].Tool
	if patch.Name != "Edit" || patch.Summary != "app/main.go" || patch.Status != chat.ToolDone || patch.Output != "Success." {
		t.Errorf("patch = %+v", patch)
	}
	plan := entries[6].Tool
	if plan.Status != chat.ToolRunning || len(plan.Todos) != 2 || plan.Todos[1].Content != "Fix" || plan.Todos[1].Status != "in_progress" {
		t.Errorf("plan = %+v", plan)
	}
	if entries[7].Text != "Interrupted" {
		t.Errorf("notice = %+v", entries[7])
	}
}

func TestOlderLinesAreIgnoredOnceTypedItemsAppear(t *testing.T) {
	transcript := NewTranscript()
	changed := feedAll(transcript,
		`{"timestamp":"T1","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"hi"}]}}}`,
		`{"timestamp":"T2","type":"event_msg","payload":{"type":"agent_message","message":"hi again"}}`,
		`{"timestamp":"T3","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"}}`,
	)
	if len(changed) != 1 || changed[0].ID != "u1" {
		t.Errorf("changed = %+v", changed)
	}
}

func TestBrokenLinesAreSkipped(t *testing.T) {
	transcript := NewTranscript()
	if changed := feedAll(transcript, `not json`, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage"}}}`, `{"type":"event_msg"}`); changed != nil {
		t.Errorf("changed = %+v", changed)
	}
}

func TestFindRolloutSearchesTheDatedFolders(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "sessions", "2026", "10", "17", "rollout-2026-10-17T20-12-45-abc-123.jsonl")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindRollout(home, "abc-123"); !ok || got != want {
		t.Errorf("FindRollout = %q, %v", got, ok)
	}
	if _, ok := FindRollout(home, "other"); ok {
		t.Error("found a rollout for an unknown thread")
	}
	if _, ok := FindRollout(home, "../../etc/passwd"); ok {
		t.Error("accepted a thread id with path characters")
	}
}
