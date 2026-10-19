package host

import (
	"maps"
	"slices"
	"testing"
)

func TestParseListeningProcessesGroupsPortsByPid(t *testing.T) {
	output := "p13795\nf4\nn127.0.0.1:47200\np900\nf3\nn*:3000\nf5\nn[::1]:3000\nf6\nn[::1]:5173\n"
	got := ParseListeningProcesses([]byte(output))
	if !slices.Equal(got[13795], []int{47200}) || !slices.Equal(got[900], []int{3000, 5173}) || len(got) != 2 {
		t.Fatalf("listening processes = %v", got)
	}
}

func TestParseParentProcesses(t *testing.T) {
	got := ParseParentProcesses([]byte("    1     0\n  102   101\nbad line here\n"))
	if !maps.Equal(got, map[int]int{1: 0, 102: 101}) {
		t.Fatalf("parents = %v", got)
	}
}
