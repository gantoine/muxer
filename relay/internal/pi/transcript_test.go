package pi

import (
	"encoding/base64"
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

func TestMessagesBecomeEntries(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"session","version":3,"id":"019f1aea","timestamp":"T0","cwd":"/Users/me/app"}`,
		`{"type":"model_change","id":"m1","parentId":null,"timestamp":"T0","provider":"anthropic","modelId":"claude-haiku-4-5"}`,
		`{"type":"message","id":"u1","parentId":"m1","timestamp":"T1","message":{"role":"user","content":[{"type":"text","text":"/var/T/clipboard-2026-10-20-073904-C7B83C33.jpeg what is this"}]}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"T2","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Looking at it"},{"type":"text","text":"A blue screen."}],"stopReason":"stop"}}`,
		`{"type":"message","id":"u2","parentId":"a1","timestamp":"T3","message":{"role":"user","content":"plain string"}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 4 {
		t.Fatalf("entries = %+v", entries)
	}
	user := entries[0]
	if user.Kind != chat.KindUser || user.Text != "what is this" || len(user.Images) != 1 || user.Images[0] != "/var/T/clipboard-2026-10-20-073904-C7B83C33.jpeg" || user.Time != "T1" {
		t.Errorf("user = %+v", user)
	}
	if entries[1].Kind != chat.KindThinking || entries[1].Text != "Looking at it" {
		t.Errorf("thinking = %+v", entries[1])
	}
	if entries[2].Kind != chat.KindAssistant || entries[2].Text != "A blue screen." || entries[2].ID != "a1:1" {
		t.Errorf("assistant = %+v", entries[2])
	}
	if entries[3].Kind != chat.KindUser || entries[3].Text != "plain string" {
		t.Errorf("string content = %+v", entries[3])
	}
}

func TestToolCallsAreCompletedByTheirResults(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"T1","message":{"role":"assistant","content":[`+
			`{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"ls -la\necho done"}},`+
			`{"type":"toolCall","id":"c2","name":"edit","arguments":{"path":"/Users/me/app/src/notes.txt","edits":[{"oldText":"world","newText":"there"}]}},`+
			`{"type":"toolCall","id":"c3","name":"write","arguments":{"path":"/Users/me/app/todo.md","content":"one\ntwo\n"}},`+
			`{"type":"toolCall","id":"c4","name":"bash","arguments":{"command":"cat missing.txt"}},`+
			`{"type":"toolCall","id":"c5","name":"scouts_list","arguments":{}}`+
			`],"stopReason":"toolUse"}}`,
		`{"type":"message","id":"r1","parentId":"a1","timestamp":"T2","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":"a.txt\ndone\n"}],"isError":false}}`,
		`{"type":"message","id":"r2","parentId":"r1","timestamp":"T2","message":{"role":"toolResult","toolCallId":"c2","toolName":"edit","content":[{"type":"text","text":"Successfully replaced 1 block(s)."}],"details":{"diff":"","patch":"Index: notes.txt\n===\n--- notes.txt\n+++ notes.txt\n@@ -1,2 +1,2 @@\n hello\n-world\n+there\n"},"isError":false}}`,
		`{"type":"message","id":"r3","parentId":"r2","timestamp":"T2","message":{"role":"toolResult","toolCallId":"c4","toolName":"bash","content":[{"type":"text","text":"cat: missing.txt: No such file or directory\n\nCommand exited with code 1"}],"isError":true}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 5 {
		t.Fatalf("entries = %+v", entries)
	}
	shell := entries[0].Tool
	if shell.Name != "Bash" || shell.Summary != "ls -la" || shell.Status != chat.ToolDone || shell.Output != "a.txt\ndone" {
		t.Errorf("shell = %+v", shell)
	}
	edit := entries[1].Tool
	if edit.Name != "Edit" || edit.Summary != "src/notes.txt" || edit.Status != chat.ToolDone || len(edit.Diff) != 1 || len(edit.Diff[0].Lines) != 3 || edit.Diff[0].OldStart != 1 {
		t.Errorf("edit = %+v", edit)
	}
	write := entries[2].Tool
	if write.Name != "Write" || write.Summary != "app/todo.md" || write.Status != chat.ToolRunning || len(write.Diff) != 1 || write.Diff[0].Lines[1] != "+two" {
		t.Errorf("write = %+v", write)
	}
	if failed := entries[3].Tool; failed.Status != chat.ToolFailed {
		t.Errorf("failed = %+v", failed)
	}
	if custom := entries[4].Tool; custom.Name != "scouts_list" || custom.Status != chat.ToolRunning {
		t.Errorf("custom = %+v", custom)
	}
}

func TestStopsErrorsAndUserCommandsBecomeEntries(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"message","id":"a1","parentId":null,"timestamp":"T1","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"No API key for provider: anthropic"}}`,
		`{"type":"message","id":"a2","parentId":"a1","timestamp":"T2","message":{"role":"assistant","content":[{"type":"text","text":"Partial"}],"stopReason":"aborted"}}`,
		`{"type":"message","id":"b1","parentId":"a2","timestamp":"T3","message":{"role":"bashExecution","command":"git status","output":"clean\n","exitCode":0,"cancelled":false,"truncated":false}}`,
		`{"type":"compaction","id":"c1","parentId":"b1","timestamp":"T4","summary":"Earlier work","firstKeptEntryId":"b1","tokensBefore":50000}`,
	)
	entries := transcript.Entries()
	if len(entries) != 5 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Kind != chat.KindNotice || entries[0].Text != "No API key for provider: anthropic" {
		t.Errorf("error = %+v", entries[0])
	}
	if entries[2].Kind != chat.KindNotice || entries[2].Text != "Interrupted" {
		t.Errorf("aborted = %+v", entries[2])
	}
	if command := entries[3].Tool; command.Name != "Bash" || command.Summary != "git status" || command.Status != chat.ToolDone || command.Output != "clean" {
		t.Errorf("user command = %+v", entries[3])
	}
	if entries[4].Kind != chat.KindNotice || entries[4].Text != "Compacted the conversation" {
		t.Errorf("compaction = %+v", entries[4])
	}
}

func TestGoingBackToAnEarlierEntryIsNoted(t *testing.T) {
	transcript := NewTranscript()
	changed := feedAll(transcript,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"T1","message":{"role":"user","content":"first"}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"T2","message":{"role":"assistant","content":[{"type":"text","text":"one"}],"stopReason":"stop"}}`,
		`{"type":"label","id":"l1","parentId":"a1","timestamp":"T3","targetId":"u1","label":"start"}`,
		`{"type":"message","id":"u2","parentId":"u1","timestamp":"T4","message":{"role":"user","content":"again"}}`,
	)
	if len(changed) != 4 || changed[2].Kind != chat.KindNotice || changed[2].ID != "branch:u2" || changed[3].Text != "again" {
		t.Fatalf("changed = %+v", changed)
	}
}

func TestBrokenLinesAreSkipped(t *testing.T) {
	transcript := NewTranscript()
	if changed := feedAll(transcript, `not json`, `{"type":"message","id":"x","message":"nope"}`, `{"type":"message","id":"r","parentId":"x","message":{"role":"toolResult","toolCallId":"missing"}}`); len(changed) != 0 {
		t.Fatalf("changed = %+v", changed)
	}
}

func TestFindSessionSearchesEveryFolder(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "sessions", "--Users-me-app--", "2026-10-20T11-36-53-410Z_01a15919-9da2.jsonl")
	if err := os.MkdirAll(filepath.Dir(session), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(session, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if found, ok := FindSession(dir, "01a15919-9da2"); !ok || found != session {
		t.Fatalf("found = %q, %v", found, ok)
	}
	for _, id := range []string{"", "missing", "../escape", "*"} {
		if _, ok := FindSession(dir, id); ok {
			t.Errorf("id %q must not match", id)
		}
	}
}

func TestReadImageFindsTheBlockOfThatEntry(t *testing.T) {
	session := filepath.Join(t.TempDir(), "session.jsonl")
	photo := base64.StdEncoding.EncodeToString([]byte("png bytes"))
	lines := `{"type":"message","id":"a0","parentId":null,"message":{"role":"assistant","content":[{"type":"text","text":"mentions \"id\":\"u1\""}]}}` + "\n" +
		`{"type":"message","id":"u1","parentId":"a0","message":{"role":"user","content":[{"type":"text","text":"look"},{"type":"image","data":"` + photo + `","mimeType":"image/png"}]}}` + "\n"
	if err := os.WriteFile(session, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	image, err := ReadImage(session, "u1:1")
	if err != nil || image.Data != photo || image.MediaType != "image/png" {
		t.Fatalf("image = %+v, %v", image, err)
	}
	for _, ref := range []string{"u1:0", "u1:2", "a0:0", "u1", "u1:1:0"} {
		if _, err := ReadImage(session, ref); err == nil {
			t.Errorf("ref %q must not find an image", ref)
		}
	}
}
