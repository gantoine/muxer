package chat

import (
	"os"
	"path/filepath"
	"testing"
)

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
