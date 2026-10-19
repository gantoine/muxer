package server

import (
	"context"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFeedPollsOnceForEveryWatcherAndStopsWhenNobodyWatches(t *testing.T) {
	var polls atomic.Int64
	value := atomic.Value{}
	value.Store("a")
	f := newFeed(time.Hour, func(context.Context) ([]byte, bool) {
		polls.Add(1)
		return []byte(value.Load().(string)), true
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first, leaveFirst := f.subscribe(ctx)
	<-first
	if string(f.current()) != "a" {
		t.Fatalf("the first watcher must see the polled value, got %q", f.current())
	}
	_, leaveSecond := f.subscribe(ctx)
	waitFor(t, func() bool { return polls.Load() == 2 })
	select {
	case <-first:
		t.Fatal("an unchanged value must not be announced again")
	default:
	}

	value.Store("b")
	f.refresh()
	<-first
	if string(f.current()) != "b" {
		t.Fatalf("watchers must see the new value, got %q", f.current())
	}

	leaveFirst()
	leaveSecond()
	f.refresh()
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return !f.running
	})
	if f.current() != nil {
		t.Fatal("a stopped feed must not hand out a stale value")
	}
	stoppedAt := polls.Load()
	f.refresh()
	time.Sleep(20 * time.Millisecond)
	if polls.Load() != stoppedAt {
		t.Fatal("a feed nobody watches must stop polling")
	}
}

func TestPhonesShareOneWorkspacePoller(t *testing.T) {
	relay := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for index := range 3 {
		conn, _, err := relay.dial(t, ctx, relay.pair(t))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": strconv.Itoa(index)})
		readUntil(t, ctx, conn, "workspaces")
	}
	relay.cmux.mu.Lock()
	reads := relay.cmux.workspaceReads
	relay.cmux.mu.Unlock()
	if reads > 3 {
		t.Fatalf("each new phone may trigger at most one extra poll, got %d reads", reads)
	}
}

func TestListedMethodsDecideWhatTheRelayAsks(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.mu.Lock()
	relay.cmux.methods = []string{"system.identify", "mobile.workspace.list"}
	relay.cmux.mu.Unlock()
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	welcome := resultFor(t, ctx, conn, "1")
	unsupported := welcome["data"].(map[string]any)["unsupported"].([]any)
	for _, kind := range []any{"keep_awake", "top", "checklist", "close_terminal", "set_vm_paused", "start_agent_task", "port_link", "expose_port"} {
		if !slices.Contains(unsupported, kind) {
			t.Fatalf("the welcome must list %v as unsupported: %v", kind, unsupported)
		}
	}
	readUntil(t, ctx, conn, "workspaces")
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "keep_awake", "id": "2"})
	if result := resultFor(t, ctx, conn, "2"); result["ok"] != false || errorCode(result) != "unsupported" {
		t.Fatalf("an unlisted method must be reported as unsupported: %v", result)
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if relay.cmux.keepAwakeReads != 0 || relay.cmux.gitReads != 0 {
		t.Fatalf("the relay must not call unlisted methods: %d keep-awake, %d git reads", relay.cmux.keepAwakeReads, relay.cmux.gitReads)
	}
}

func TestMethodSupportFallsBackToMissingMethodErrors(t *testing.T) {
	var support methodSupport
	support.load(context.Background(), &fakeCmux{})
	if !support.supports(cmux.MethodKeepAwake) {
		t.Fatal("without a method list every method must be tried")
	}
	support.observe(cmux.MethodKeepAwake, &cmux.RPCError{Code: "method_not_found"})
	if support.supports(cmux.MethodKeepAwake) {
		t.Fatal("a method cmux rejected must not be asked again")
	}
	support.load(context.Background(), &fakeCmux{methods: []string{cmux.MethodKeepAwake}})
	if !support.supports(cmux.MethodKeepAwake) || support.supports(cmux.MethodTerminalGit) {
		t.Fatal("a fresh method list must replace what was learned from errors")
	}
}
