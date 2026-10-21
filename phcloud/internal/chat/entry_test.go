package chat

import "testing"

func TestLongDiffsAreCapped(t *testing.T) {
	lines := make([]string, MaxDiffLines+50)
	for i := range lines {
		lines[i] = "+line"
	}
	hunks := CapHunks([]Hunk{{OldStart: 1, NewStart: 1, Lines: lines}, {OldStart: 900, NewStart: 900, Lines: []string{"-x"}}})
	if len(hunks) != 1 || len(hunks[0].Lines) != MaxDiffLines {
		t.Fatalf("hunks = %d, first has %d lines", len(hunks), len(hunks[0].Lines))
	}
}
