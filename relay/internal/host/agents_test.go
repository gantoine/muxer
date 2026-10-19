package host

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFoundAgentsReadsOnlyTheMarkedLines(t *testing.T) {
	output := []byte("Welcome back!\nmuxer-agent:codex\nmuxer-agent:pi\n  muxer-agent:claude  \nmuxer-agent:codex\n")
	if got := FoundAgents(output, []string{"claude", "codex"}); !slices.Equal(got, []string{"claude", "codex"}) {
		t.Fatalf("found = %v", got)
	}
	if got := FoundAgents([]byte("nothing\n"), []string{"claude"}); got == nil || len(got) != 0 {
		t.Fatalf("no agent must be an empty list, got %#v", got)
	}
}

func TestAgentFinderLooksAgentsUpInTheLoginShell(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	finder := NewAgentFinder("/bin/sh", "claude", "muxer-missing-agent")
	found, err := finder.Installed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(found, []string{"claude"}) {
		t.Fatalf("found = %v", found)
	}
}

func TestAgentFinderRemembersABrokenShellForAWhile(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")
	shell := filepath.Join(t.TempDir(), "broken-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\necho run >> "+runs+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	finder := NewAgentFinder(shell, "claude")
	for range 3 {
		if _, err := finder.Installed(context.Background()); err == nil {
			t.Fatal("a failing shell must report its error")
		}
	}
	if logged, _ := os.ReadFile(runs); string(logged) != "run\n" {
		t.Fatalf("the shell must run once while its failure is remembered, ran: %q", logged)
	}
}
