//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gantoine/muxer/relay/internal/agents"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/server"
)

type eventLog struct {
	mu     sync.Mutex
	events []cmux.Event
}

func (l *eventLog) await(t *testing.T, what string, matches func(cmux.Event) bool) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, event := range l.events {
			if matches(event) {
				l.mu.Unlock()
				return
			}
		}
		l.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("cmux never published %s", what)
}

func subscribe(t *testing.T, filter cmux.EventFilter) *eventLog {
	t.Helper()
	log := &eventLog{}
	ctx, cancel := context.WithCancel(context.Background())
	client := cmuxClient(t)
	go func() {
		_ = client.Events(ctx, nil, filter, func(event cmux.Event) error {
			log.mu.Lock()
			log.events = append(log.events, event)
			log.mu.Unlock()
			return nil
		})
	}()
	t.Cleanup(cancel)
	time.Sleep(time.Second)
	return log
}

func named(name, workspaceID string) func(cmux.Event) bool {
	return func(event cmux.Event) bool { return event.Name == name && event.Workspace() == workspaceID }
}

func TestCmuxPublishesTheEventsTheRelayListensFor(t *testing.T) {
	categories := slices.Concat([]string{"notification"}, agents.EventCategories, server.WorkspaceEventCategories)
	log := subscribe(t, cmux.EventFilter{Categories: categories})

	t.Run("workspaces", func(t *testing.T) {
		client := cmuxClient(t)
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		created, err := client.CreateWorkspace(ctx, uniqueName(t), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { closeWorkspace(created.WorkspaceID) })
		log.await(t, "workspace.created", named("workspace.created", created.WorkspaceID))

		if err := client.RenameWorkspace(ctx, created.WorkspaceID, uniqueName(t)); err != nil {
			t.Fatal(err)
		}
		log.await(t, "workspace.renamed", named("workspace.renamed", created.WorkspaceID))

		if err := client.Call(ctx, cmux.MethodWorkspaceAction, map[string]any{"workspace_id": created.WorkspaceID, "action": "pin"}, nil); err != nil {
			t.Fatal(err)
		}
		log.await(t, "workspace.action", named("workspace.action", created.WorkspaceID))
		if err := client.Call(ctx, cmux.MethodWorkspaceAction, map[string]any{"workspace_id": created.WorkspaceID, "action": "unpin"}, nil); err != nil {
			t.Fatal(err)
		}

		if err := client.CloseWorkspace(ctx, created.WorkspaceID, true); err != nil {
			t.Fatal(err)
		}
		log.await(t, "workspace.closed", named("workspace.closed", created.WorkspaceID))
	})

	t.Run("notifications", func(t *testing.T) {
		p := connect(t)
		ws := scratchWorkspace(t, p)
		mentions := func(name, id string) func(cmux.Event) bool {
			return func(event cmux.Event) bool { return event.Name == name && strings.Contains(string(event.Payload), id) }
		}

		read := notify(t, p, ws, uniqueName(t)+"-read")
		log.await(t, "notification.created", mentions("notification.created", read))
		p.must("mark_notifications_read", map[string]any{"notification_id": read})
		log.await(t, "notification.read", mentions("notification.read", read))

		cleared := notify(t, p, ws, uniqueName(t)+"-clear")
		cmuxCLI(t, "clear-notifications", "--workspace", ws.ID, "--surface", ws.SurfaceID)
		log.await(t, "notification.cleared", mentions("notification.cleared", cleared))
	})
}

func TestRecordCmuxResponses(t *testing.T) {
	p := connect(t)
	ws := scratchWorkspace(t, p)
	attach(t, p, ws, ws.SurfaceID)
	client := cmuxClient(t)
	terminal := map[string]any{"workspace_id": ws.ID, "surface_id": ws.SurfaceID}
	calls := []struct {
		method   string
		params   any
		optional bool
	}{
		{cmux.MethodIdentify, nil, false},
		{cmux.MethodCapabilities, nil, false},
		{cmux.MethodHostStatus, nil, false},
		{cmux.MethodWorkspaceList, nil, false},
		{cmux.MethodGroupList, map[string]any{}, false},
		{cmux.MethodNotificationList, nil, false},
		{cmux.MethodTop, map[string]any{"include_processes": true}, false},
		{cmux.MethodKeepAwake, nil, false},
		{cmux.MethodVaultSessions, map[string]any{"limit": 5}, false},
		{cmux.MethodTerminalGit, nil, false},
		{cmux.MethodLaneStatus, map[string]any{"workspace_id": ws.ID}, false},
		{cmux.MethodChecklist, map[string]any{"workspace_id": ws.ID}, false},
		{cmux.MethodRemoteStatus, map[string]any{"workspace_id": ws.ID}, false},
		{cmux.MethodReplay, terminal, false},
		{cmux.MethodInputState, terminal, false},
		{cmux.MethodFeedList, nil, false},
		{cmux.MethodAgentSession, terminal, true},
		{cmux.MethodVMList, nil, true},
	}
	dir := os.Getenv("E2E_SNAPSHOT_DIR")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range calls {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		var result json.RawMessage
		err := client.Call(ctx, call.method, call.params, &result)
		cancel()
		if err != nil && !call.optional {
			t.Errorf("%s failed: %v", call.method, err)
		}
		if dir == "" {
			continue
		}
		record := map[string]any{"method": call.method, "params": call.params, "result": result}
		if err != nil {
			record["error"] = err.Error()
		}
		encoded, _ := json.MarshalIndent(record, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, call.method+".json"), encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
