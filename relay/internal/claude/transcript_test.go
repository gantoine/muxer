package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func feedAll(t *Transcript, lines ...string) []Entry {
	var changed []Entry
	for _, raw := range lines {
		changed = append(changed, t.Feed([]byte(raw))...)
	}
	return changed
}

func TestTranscriptTurnsMessagesIntoEntries(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"summary","summary":"ignored"}`,
		`{"type":"user","uuid":"u1","timestamp":"t1","message":{"role":"user","content":"fix the tests"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Running them now."}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./...\necho done","description":"Run tests"}}]}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok  all","is_error":false}]}}`,
		`{"type":"assistant","uuid":"a3","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent chatter"}]}}`,
		`{"type":"user","uuid":"u3","isMeta":true,"message":{"role":"user","content":"Caveat: meta"}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	if entries[0].Kind != KindUser || entries[0].Text != "fix the tests" || entries[0].Time != "t1" {
		t.Errorf("user entry = %+v", entries[0])
	}
	if entries[1].Kind != KindAssistant || entries[1].Text != "Running them now." {
		t.Errorf("assistant entry = %+v", entries[1])
	}
	tool := entries[2]
	if tool.ID != "toolu_1" || tool.Tool.Name != "Bash" || tool.Tool.Summary != "go test ./..." || tool.Tool.Status != ToolDone || tool.Tool.Output != "ok  all" {
		t.Errorf("tool entry = %+v %+v", tool, tool.Tool)
	}
}

func TestToolResultsUpdateTheirToolEntry(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript, `{"type":"assistant","uuid":"a","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/Users/me/app/src/main.go"}}]}}`)
	changed := transcript.Feed([]byte(`{"type":"user","uuid":"u","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"no such file"}]}]}}`))
	if len(changed) != 1 || changed[0].ID != "t1" || changed[0].Tool.Status != ToolFailed || changed[0].Tool.Output != "no such file" || changed[0].Tool.Summary != "src/main.go" {
		t.Fatalf("changed = %+v", changed)
	}
	if len(transcript.Entries()) != 1 {
		t.Fatalf("result added a new entry instead of updating: %+v", transcript.Entries())
	}
}

func TestSlashCommandsAndLocalOutput(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"user","uuid":"c","message":{"role":"user","content":"<command-message>model</command-message>\n<command-name>/model</command-name>\n<command-args>opus</command-args>"}}`,
		`{"type":"user","uuid":"o","message":{"role":"user","content":"<local-command-stdout>Set model to Opus</local-command-stdout>"}}`,
		`{"type":"user","uuid":"i","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
		`{"type":"user","uuid":"r","message":{"role":"user","content":"<system-reminder>hidden</system-reminder>"}}`,
	)
	entries := transcript.Entries()
	want := []struct{ kind, text string }{{KindUser, "/model opus"}, {KindNotice, "Set model to Opus"}, {KindNotice, "Interrupted"}}
	if len(entries) != len(want) {
		t.Fatalf("got %+v", entries)
	}
	for i, w := range want {
		if entries[i].Kind != w.kind || entries[i].Text != w.text {
			t.Errorf("entry %d = %+v, want %s %q", i, entries[i], w.kind, w.text)
		}
	}
}

func TestBrokenLinesAreSkipped(t *testing.T) {
	transcript := NewTranscript()
	if changed := transcript.Feed([]byte(`{"type":"user","message":`)); changed != nil {
		t.Fatalf("changed = %+v", changed)
	}
}

func TestFindTranscriptUsesTheEncodedProjectFolder(t *testing.T) {
	config := t.TempDir()
	want := filepath.Join(config, "projects", "-Users-me-my-app", "abc-123.jsonl")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := FindTranscript(config, "/Users/me/my.app", "abc-123"); !ok || got != want {
		t.Errorf("FindTranscript = %q, %v", got, ok)
	}
	if got, ok := FindTranscript(config, "/somewhere/else", "abc-123"); !ok || got != want {
		t.Errorf("fallback FindTranscript = %q, %v", got, ok)
	}
	if _, ok := FindTranscript(config, "/Users/me/my.app", "../../etc/passwd"); ok {
		t.Error("accepted a session id with path characters")
	}
}

func TestTailReturnsWholeLinesAndNoticesTruncation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	tail := NewTail(file, 0)
	if lines, _, err := tail.Lines(); err != nil || lines != nil {
		t.Fatalf("missing file: %q %v", lines, err)
	}
	write := func(content string, appendTo bool) {
		flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if appendTo {
			flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(file, flags, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(content)
		f.Close()
	}
	write("one\ntw", false)
	lines, _, _ := tail.Lines()
	if len(lines) != 1 || string(lines[0]) != "one" {
		t.Fatalf("first read = %q", lines)
	}
	write("o\nthree\n", true)
	lines, _, _ = tail.Lines()
	if len(lines) != 2 || string(lines[0]) != "two" || string(lines[1]) != "three" {
		t.Fatalf("second read = %q", lines)
	}
	write("new\n", false)
	lines, restarted, _ := tail.Lines()
	if !restarted || len(lines) != 1 || string(lines[0]) != "new" {
		t.Fatalf("after truncation = %q restarted=%v", lines, restarted)
	}
}

func TestTailStartsNearTheEndOfALongFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(file, []byte("first line\nsecond\nthird\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tail := NewTail(file, 10)
	lines, _, err := tail.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || string(lines[0]) != "third" {
		t.Fatalf("only whole lines inside the window must be read, got %q", lines)
	}
	f, _ := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("fourth\n")
	f.Close()
	if lines, _, _ = tail.Lines(); len(lines) != 1 || string(lines[0]) != "fourth" {
		t.Fatalf("later reads continue from where the first stopped, got %q", lines)
	}
}

func TestImagesSentFromThePhoneBecomeAttachments(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"user","uuid":"one","message":{"role":"user","content":"/var/folders/xr/abc/T/clipboard-2026-10-17-084701-6FFF738B.jpeg Describe this image"}}`,
		`{"type":"user","uuid":"two","message":{"role":"user","content":"/var/folders/xr/abc/T/clipboard-2026-10-17-084701-6FFF738B.jpeg /var/folders/xr/abc/T/clipboard-2026-10-17-084702-0A1B2C3D.png"}}`,
		`{"type":"user","uuid":"plain","message":{"role":"user","content":"/tmp/screenshot.png looks off"}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %+v", entries)
	}
	if entries[0].Text != "Describe this image" || len(entries[0].Images) != 1 || entries[0].Images[0] != "/var/folders/xr/abc/T/clipboard-2026-10-17-084701-6FFF738B.jpeg" {
		t.Errorf("image with text = %+v", entries[0])
	}
	if entries[1].Text != "" || len(entries[1].Images) != 2 || entries[1].Images[1] != "/var/folders/xr/abc/T/clipboard-2026-10-17-084702-0A1B2C3D.png" {
		t.Errorf("images alone = %+v", entries[1])
	}
	if entries[2].Text != "/tmp/screenshot.png looks off" || entries[2].Images != nil {
		t.Errorf("typed paths must stay text = %+v", entries[2])
	}
}

func TestImagesInMessagesAndToolResultsBecomeReferences(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"[Image #3] why is this orange?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},{"type":"text","text":"[Image: source: /tmp/shot.png]"}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/tmp/shot.png"}}]}}`,
		`{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"seen"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}}`,
	)
	entries := transcript.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %+v", entries)
	}
	if entries[0].Text != "why is this orange?" || len(entries[0].Images) != 1 || entries[0].Images[0] != "u1:1" {
		t.Errorf("pasted image = %+v", entries[0])
	}
	if entries[1].Text != "" || len(entries[1].Images) != 1 || entries[1].Images[0] != "u2:0" {
		t.Errorf("image without text = %+v", entries[1])
	}
	if tool := entries[2].Tool; tool.Output != "seen" || len(tool.Images) != 1 || tool.Images[0] != "r1:0:1" {
		t.Errorf("tool result = %+v", tool)
	}
}

func TestReadImageFindsTheBlockAReferenceNames(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	content := `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"SU1H"}}]}}` + "\n" +
		`{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"UE5H"}}]}]}}` + "\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if image, err := ReadImage(file, "u1:1"); err != nil || image.Data != "SU1H" || image.MediaType != "image/jpeg" {
		t.Errorf("message image = %+v, %v", image, err)
	}
	if image, err := ReadImage(file, "r1:0:0"); err != nil || image.Data != "UE5H" || image.MediaType != "image/png" {
		t.Errorf("tool result image = %+v, %v", image, err)
	}
	for _, ref := range []string{"u1:0", "u1:5", "missing:0", "../etc:0", "r1:0"} {
		if _, err := ReadImage(file, ref); err == nil {
			t.Errorf("ReadImage(%q) found something", ref)
		}
	}
}

func TestReadingAnImageFromThePhoneDoesNotRepeatIt(t *testing.T) {
	transcript := NewTranscript()
	feedAll(transcript,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/var/folders/xr/abc/T/clipboard-2026-10-17-084701-6FFF738B.jpeg"}}]}}`,
		`{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"AAAA"}}]}]}}`,
	)
	if tool := transcript.Entries()[0].Tool; tool.Status != ToolDone || tool.Images != nil {
		t.Errorf("tool = %+v", tool)
	}
}
