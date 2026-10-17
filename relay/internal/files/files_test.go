package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRankPrefersNameMatchesThenShallowerPaths(t *testing.T) {
	paths := []string{"app/src/ChatView.kt", "docs/chat.md", "chat/", "relay/internal/server/chat.go", "Chat.go", "notes.txt", "app/src/CopyHelper.kt"}
	got := Rank(paths, "chat", 10)
	want := []string{"chat/", "Chat.go", "docs/chat.md", "app/src/ChatView.kt", "relay/internal/server/chat.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rank = %v, want %v", got, want)
	}
	if got := Rank(paths, "acv", 10); !reflect.DeepEqual(got, []string{"app/src/ChatView.kt"}) {
		t.Fatalf("subsequence match = %v", got)
	}
	if got := Rank(paths, "", 2); !reflect.DeepEqual(got, []string{"chat/", "Chat.go"}) {
		t.Fatalf("empty query = %v", got)
	}
}

func TestWalkSkipsHiddenAndBuildFolders(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"main.go", "src/app.go", ".env", ".git/config", "node_modules/x/index.js", "build/out.bin"} {
		write(t, filepath.Join(root, name))
	}
	got, err := NewIndex().Search(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"src/", "main.go", "src/app.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search = %v, want %v", got, want)
	}
}

func TestGitFoldersHonorIgnoreFilesAndCache(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	write(t, filepath.Join(root, ".gitignore"), "secret.txt\n")
	write(t, filepath.Join(root, "secret.txt"))
	write(t, filepath.Join(root, "kept.txt"))
	index := NewIndex()
	clock := time.Unix(0, 0)
	index.now = func() time.Time { return clock }
	got, _ := index.Search(context.Background(), root, "txt")
	if !reflect.DeepEqual(got, []string{"kept.txt"}) {
		t.Fatalf("Search = %v", got)
	}
	write(t, filepath.Join(root, "later.txt"))
	if got, _ := index.Search(context.Background(), root, "later"); len(got) != 0 {
		t.Fatalf("a fresh listing must come from the cache: %v", got)
	}
	clock = clock.Add(cacheTTL)
	if got, _ := index.Search(context.Background(), root, "later"); !reflect.DeepEqual(got, []string{"later.txt"}) {
		t.Fatalf("an old listing must be refreshed: %v", got)
	}
}

func TestSearchNeedsAnAbsoluteFolder(t *testing.T) {
	if _, err := NewIndex().Search(context.Background(), "", "x"); err == nil {
		t.Fatal("an empty folder must fail")
	}
}

func write(t *testing.T, name string, content ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	data := ""
	if len(content) > 0 {
		data = content[0]
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
