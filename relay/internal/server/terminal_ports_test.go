package server

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const surfaceTopFixture = `{"windows":[{"workspaces":[{"id":"w1","panes":[{"surfaces":[
  {"id":"t1","processes":[{"pid":100,"name":"zsh","children":[{"pid":101,"name":"npm"}]}]},
  {"id":"t2","processes":[{"pid":13795,"name":"Python"}]}
]}]}]}]}`

func fakeTop(t *testing.T) cmux.Top {
	t.Helper()
	var top cmux.Top
	if err := json.Unmarshal([]byte(surfaceTopFixture), &top); err != nil {
		t.Fatal(err)
	}
	return top
}

func TestListenersAreTracedUpToTheirTerminal(t *testing.T) {
	owners := surfaceOwners(fakeTop(t))
	parents := map[int]int{102: 101, 101: 100, 100: 1, 13795: 1, 500: 400, 400: 1, 700: 700}
	listening := map[int][]int{102: {5173, 3000}, 13795: {47200}, 500: {9999}, 700: {7000}, 101: {3000}}
	got := portsBySurface(owners, parents, listening)
	if !slices.Equal(got["t1"], []int{3000, 5173}) {
		t.Fatalf("a grandchild of the shell must count for its terminal: %v", got)
	}
	if !slices.Equal(got["t2"], []int{47200}) {
		t.Fatalf("a process cmux lists for a terminal must count for it: %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("listeners outside cmux must be ignored: %v", got)
	}
}

func TestDiscoveredPortsFillTheWorkspaceListAndTheAllowList(t *testing.T) {
	listening := map[int][]int{102: {5173}}
	relay := startRelayWith(t, func(options *Options) {
		options.ListeningProcesses = func(context.Context) (map[int][]int, error) { return listening, nil }
		options.ParentProcesses = func(context.Context) (map[int]int, error) { return map[int]int{102: 101, 101: 100}, nil }
	})
	relay.cmux.mu.Lock()
	relay.cmux.top = fakeTop(t)
	relay.cmux.terminalGit = []cmux.TerminalGit{{SurfaceID: "t1"}}
	relay.cmux.mu.Unlock()
	relay.tailnet.listen(5173, "127.0.0.1")
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "hello"})
	workspaces := readUntil(t, ctx, conn, "workspaces")["workspaces"].([]any)
	terminal := workspaces[0].(map[string]any)["terminals"].([]any)[0].(map[string]any)
	if ports, _ := terminal["listening_ports"].([]any); len(ports) != 1 || ports[0] != float64(5173) {
		t.Fatalf("ports found from processes must reach the workspace list: %v", terminal)
	}
	result := ask(t, ctx, conn, "1", "port_link", 5173)
	if result["ok"] != true || result["data"].(map[string]any)["needs_expose"] != true {
		t.Fatalf("a discovered port must be allowed: %v", result)
	}
	if result := ask(t, ctx, conn, "2", "port_link", 6000); errorCode(result) != "port_not_allowed" {
		t.Fatalf("ports no terminal owns must stay refused: %v", result)
	}
}

func TestCmuxReportedPortsWinOverDiscovery(t *testing.T) {
	if got := mergedPorts([]int{3000}, []int{3000, 9000}); !slices.Equal(got, []int{3000}) {
		t.Fatalf("cmux's own ports must be used when it reports any: %v", got)
	}
	if got := mergedPorts(nil, []int{9000}); !slices.Equal(got, []int{9000}) {
		t.Fatalf("discovered ports must fill in when cmux reports none: %v", got)
	}
}

func TestPortsCmuxAlreadyReportsSkipTheProcessWalk(t *testing.T) {
	reported := map[string]cmux.TerminalGit{"t1": {SurfaceID: "t1", ListeningPorts: []int{3000}}}
	listening := map[int][]int{102: {3000}, 103: {3000, 5173}, 104: {8080}}
	got := unreportedListeners(listening, reported)
	if _, found := got[102]; found || len(got) != 2 || !slices.Equal(got[103], []int{3000, 5173}) {
		t.Fatalf("only listeners with a port cmux doesn't know must be traced: %v", got)
	}
	if got := unreportedListeners(map[int][]int{102: {3000}}, reported); len(got) != 0 {
		t.Fatalf("nothing is left to trace when cmux knows every port: %v", got)
	}
}
