package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/claude"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/devices"
	"github.com/gantoine/muxer/relay/internal/host"
)

type fakeCmux struct {
	mu              sync.Mutex
	inputs          []string
	keys            []string
	viewports       []string
	historyRequests []string
	historyGate     chan struct{}
	agent           cmux.AgentSession
	agentErr        error
	running         claude.SessionRef
	keepAwake       bool
	terminalGit     []cmux.TerminalGit
	terminalGitErr  error
	gitReads        int
	keepAwakeErr    error
	keepAwakeReads  int
	methods         []string
	workspaceReads  int
	notifications   []cmux.Notification
	markedRead      []string
	hostName        string
	hostNameErr     error
	chats           []sentChat
	chatErr         error
	terminalCalls   []string
	closeErr        error
	inputState      cmux.InputState
	inputStateErr   error
	top             cmux.Top
	topErr          error
	vmList          cmux.VMList
	vmErr           error
	vmStats         map[string]cmux.VMStats
	vmCalls         []string
	workspaceList   *cmux.WorkspaceList
	vaultSessions   []cmux.VaultSession
	vaultCalls      []string
	commands        []string
	submitted       bool
	performed       []string
	performErr      error
	performResults  map[string]string
	lanes           map[string]cmux.LaneStatus
	checklists      map[string]cmux.Checklist
	groupDetails    []cmux.GroupDetails
	remotes         map[string]cmux.RemoteState
}

type sentChat struct {
	sessionID string
	text      string
	images    []cmux.ChatImage
}

func (f *fakeCmux) HostName(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hostName, f.hostNameErr
}

func (f *fakeCmux) SendChat(_ context.Context, sessionID, text string, images []cmux.ChatImage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.chatErr != nil {
		return f.chatErr
	}
	f.chats = append(f.chats, sentChat{sessionID, text, images})
	return nil
}

func (f *fakeCmux) Notifications(context.Context) ([]cmux.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cmux.Notification(nil), f.notifications...), nil
}

func (f *fakeCmux) MarkNotificationRead(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.notifications {
		if f.notifications[i].ID == id {
			f.notifications[i].IsRead = true
			f.markedRead = append(f.markedRead, id)
			return nil
		}
	}
	return &cmux.RPCError{Code: "not_found", Message: "Notification not found"}
}

func (f *fakeCmux) MarkSurfaceNotificationsRead(_ context.Context, surfaceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.notifications {
		if !f.notifications[i].IsRead && f.notifications[i].SurfaceID == surfaceID {
			f.notifications[i].IsRead = true
			f.markedRead = append(f.markedRead, f.notifications[i].ID)
		}
	}
	return nil
}

func (f *fakeCmux) MarkAllNotificationsRead(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.notifications {
		f.notifications[i].IsRead = true
	}
	f.markedRead = append(f.markedRead, "all")
	return nil
}

func (f *fakeCmux) marked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.markedRead...)
}

func (f *fakeCmux) Top(context.Context) (cmux.Top, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.top, f.topErr
}

func (f *fakeCmux) VMs(context.Context) (cmux.VMList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vmList, f.vmErr
}

func (f *fakeCmux) VMStatus(_ context.Context, id string) (cmux.VM, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, vm := range f.vmList.VMs {
		if vm.ID == id {
			return vm, nil
		}
	}
	return cmux.VM{}, &cmux.RPCError{Code: "not_found", Message: "No such VM"}
}

func (f *fakeCmux) VMStats(_ context.Context, id string) (cmux.VMStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmCalls = append(f.vmCalls, "stats "+id)
	stats, ok := f.vmStats[id]
	if !ok {
		return stats, &cmux.RPCError{Code: "unavailable", Message: "No stats"}
	}
	return stats, nil
}

func (f *fakeCmux) SetVMPaused(_ context.Context, id string, paused bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.vmErr != nil {
		return f.vmErr
	}
	status := "running"
	if paused {
		status = "paused"
	}
	for index := range f.vmList.VMs {
		if f.vmList.VMs[index].ID == id {
			f.vmList.VMs[index].Status = status
		}
	}
	f.vmCalls = append(f.vmCalls, status+" "+id)
	return nil
}

func (f *fakeCmux) Methods(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.methods == nil {
		return nil, &cmux.RPCError{Code: "method_not_found", Message: "Unknown method"}
	}
	return f.methods, nil
}

func (f *fakeCmux) TerminalGit(context.Context) ([]cmux.TerminalGit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gitReads++
	return f.terminalGit, f.terminalGitErr
}

func (f *fakeCmux) KeepAwake(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keepAwakeReads++
	return f.keepAwake, f.keepAwakeErr
}

func (f *fakeCmux) SetKeepAwake(_ context.Context, enabled bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keepAwakeErr != nil {
		return false, f.keepAwakeErr
	}
	f.keepAwake = enabled
	return enabled, nil
}

func (f *fakeCmux) AgentSession(context.Context, string, string) (cmux.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agent, f.agentErr
}

func (f *fakeCmux) runningClaude(string) (claude.SessionRef, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, f.running.SessionID != ""
}

func (f *fakeCmux) setRunning(running claude.SessionRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = running
}

func (f *fakeCmux) setAgent(agent cmux.AgentSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agent = agent
}

func (f *fakeCmux) Identify(context.Context) (cmux.Identity, error) {
	return cmux.Identity{Version: "0.65.1"}, nil
}

func (f *fakeCmux) Workspaces(context.Context) (cmux.WorkspaceList, error) {
	f.mu.Lock()
	f.workspaceReads++
	override := f.workspaceList
	f.mu.Unlock()
	if override != nil {
		return *override, nil
	}
	return cmux.WorkspaceList{
		Workspaces: []cmux.Workspace{{ID: "w1", Title: "api", Terminals: []cmux.Terminal{{ID: "t1", Title: "zsh"}}}},
		Groups:     []cmux.WorkspaceGroup{{ID: "g1", Name: "Q4", MemberWorkspaceIDs: []string{"w1"}}},
	}, nil
}

func (f *fakeCmux) Replay(_ context.Context, workspaceID, surfaceID string) (cmux.Replay, error) {
	f.wakeTerminal(workspaceID, surfaceID)
	if surfaceID != "t1" {
		return cmux.Replay{}, &cmux.RPCError{Code: "not_found", Message: "no such terminal"}
	}
	return cmux.Replay{WorkspaceID: workspaceID, SurfaceID: surfaceID, RenderGrid: &cmux.RenderGrid{
		Columns: 80, Rows: 2,
		Styles:   []cmux.Style{{ID: 0, Foreground: "#FFFFFF", Background: "#000000"}},
		RowSpans: []cmux.Span{{Row: 0, Column: 0, StyleID: 0, Text: "$ "}},
	}}, nil
}

func (f *fakeCmux) History(_ context.Context, workspaceID, surfaceID string, maxRows int) (cmux.Replay, error) {
	f.mu.Lock()
	f.historyRequests = append(f.historyRequests, fmt.Sprintf("%s:%d", surfaceID, maxRows))
	gate := f.historyGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return cmux.Replay{WorkspaceID: workspaceID, SurfaceID: surfaceID, RenderGrid: &cmux.RenderGrid{
		Columns: 80, Rows: 2, ScrollbackRows: 3, HistoryRows: 120,
		Styles: []cmux.Style{
			{ID: 4, Foreground: "#00FF00", Background: "#000000"},
			{ID: 9, Foreground: "#FFFFFF", Background: "#000000"},
		},
		ScrollbackSpans: []cmux.Span{
			{Row: 0, Column: 0, StyleID: 9, Text: "oldest"},
			{Row: 2, Column: 0, StyleID: 9, Text: "ok "},
			{Row: 2, Column: 3, StyleID: 4, Text: "passed"},
		},
	}}, nil
}

func (f *fakeCmux) Input(_ context.Context, workspaceID, surfaceID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, workspaceID+"/"+surfaceID+":"+text)
	return nil
}

func (f *fakeCmux) SendKey(_ context.Context, _, surfaceID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, surfaceID+":"+key)
	return nil
}

func (f *fakeCmux) Paste(context.Context, string, string, string, bool) error { return nil }
func (f *fakeCmux) Scroll(context.Context, string, string, int) error         { return nil }

func (f *fakeCmux) SetViewport(_ context.Context, _, _ string, viewport cmux.Viewport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewports = append(f.viewports, "set")
	return nil
}

func (f *fakeCmux) ClearViewport(context.Context, string, string, string, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewports = append(f.viewports, "clear")
	return nil
}

func (f *fakeCmux) CreateWorkspace(context.Context, string, string) (cmux.CreatedWorkspace, error) {
	return cmux.CreatedWorkspace{WorkspaceID: "w2", SurfaceID: "t2"}, nil
}
func (f *fakeCmux) CloseWorkspace(context.Context, string, bool) error    { return nil }
func (f *fakeCmux) RenameWorkspace(context.Context, string, string) error { return nil }
func (f *fakeCmux) CreateTerminal(context.Context, string) (string, error) {
	return "t3", nil
}

func (f *fakeCmux) CloseTerminal(_ context.Context, workspaceID, surfaceID string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls = append(f.terminalCalls, fmt.Sprintf("close %s/%s force=%v", workspaceID, surfaceID, force))
	return f.closeErr
}

func (f *fakeCmux) RenameTerminal(_ context.Context, workspaceID, surfaceID, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls = append(f.terminalCalls, fmt.Sprintf("rename %s/%s %q", workspaceID, surfaceID, title))
	return nil
}

func (f *fakeCmux) RestartTerminal(_ context.Context, workspaceID, surfaceID, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalCalls = append(f.terminalCalls, fmt.Sprintf("restart %s/%s %s", workspaceID, surfaceID, command))
	return nil
}

func (f *fakeCmux) wakeTerminal(workspaceID, surfaceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workspaceList == nil {
		return
	}
	for w := range f.workspaceList.Workspaces {
		workspace := &f.workspaceList.Workspaces[w]
		for t := range workspace.Terminals {
			if workspace.ID == workspaceID && workspace.Terminals[t].ID == surfaceID {
				workspace.Terminals[t].IsReady = true
			}
		}
	}
}

func (f *fakeCmux) Call(_ context.Context, method string, params any, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	encoded, _ := json.Marshal(params)
	f.performed = append(f.performed, method+" "+string(encoded))
	if canned, ok := f.performResults[method]; ok && result != nil {
		_ = json.Unmarshal([]byte(canned), result)
	}
	return f.performErr
}

func (f *fakeCmux) LaneStatus(_ context.Context, workspaceID string) (cmux.LaneStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lanes[workspaceID], nil
}

func (f *fakeCmux) Checklist(_ context.Context, workspaceID string) (cmux.Checklist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	checklist, ok := f.checklists[workspaceID]
	if !ok {
		return cmux.Checklist{Items: []cmux.ChecklistItem{}}, nil
	}
	return checklist, nil
}

func (f *fakeCmux) GroupDetails(context.Context) ([]cmux.GroupDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupDetails, nil
}

func (f *fakeCmux) RemoteState(_ context.Context, workspaceID string) (*cmux.RemoteState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	remote, ok := f.remotes[workspaceID]
	if !ok {
		return nil, nil
	}
	return &remote, nil
}

func (f *fakeCmux) performedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.performed...)
}

func (f *fakeCmux) RunCommand(_ context.Context, workspaceID, surfaceID, command string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, fmt.Sprintf("%s/%s %s", workspaceID, surfaceID, command))
	return f.submitted, nil
}

func (f *fakeCmux) VaultSessions(_ context.Context, agent string, limit int) ([]cmux.VaultSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vaultCalls = append(f.vaultCalls, fmt.Sprintf("sessions agent=%q limit=%d", agent, limit))
	var matched []cmux.VaultSession
	for _, entry := range f.vaultSessions {
		if agent == "" || entry.Agent == agent {
			matched = append(matched, entry)
		}
	}
	return matched, nil
}

func (f *fakeCmux) VaultSearch(_ context.Context, query string, limit int) ([]cmux.VaultSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vaultCalls = append(f.vaultCalls, fmt.Sprintf("search %q limit=%d", query, limit))
	var matched []cmux.VaultSession
	for _, entry := range f.vaultSessions {
		if strings.Contains(entry.Title, query) {
			matched = append(matched, entry)
		}
	}
	return matched, nil
}

func (f *fakeCmux) InputState(context.Context, string, string) (cmux.InputState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inputState, f.inputStateErr
}

func (f *fakeCmux) snapshot() (inputs, keys, viewports []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.inputs...), append([]string(nil), f.keys...), append([]string(nil), f.viewports...)
}

type testRelay struct {
	url       string
	cmux      *fakeCmux
	store     *devices.Store
	server    *Server
	claudeDir string
	codexDir  string
}

func startRelay(t *testing.T) testRelay {
	t.Helper()
	store, err := devices.Open(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeCmux{}
	logger := slog.New(slog.DiscardHandler)
	intervals := DefaultIntervals
	intervals.Fast = 10 * time.Millisecond
	intervals.Chat = 10 * time.Millisecond
	claudeDir := t.TempDir()
	codexDir := t.TempDir()
	relay := New(fake, store, nil, Options{
		MacName: "Test Mac", RelayVersion: "test", PollInterval: intervals, ClaudeConfigDir: claudeDir,
		ClaudeSession: fake.runningClaude, CodexHome: codexDir,
		HostUsage: func(context.Context) (host.Usage, error) {
			return host.Usage{CPUPercent: 23.5, MemoryUsedBytes: 30 << 30, MemoryTotalBytes: 48 << 30}, nil
		},
	}, logger)
	server := httptest.NewServer(relay.Handler())
	t.Cleanup(server.Close)
	return testRelay{url: server.URL, cmux: fake, store: store, server: relay, claudeDir: claudeDir, codexDir: codexDir}
}

func (r testRelay) pair(t *testing.T) string {
	t.Helper()
	code, _, err := r.store.NewPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "device_name": "Pixel"})
	response, err := http.Post(r.url+"/v1/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var paired struct {
		Token   string `json:"token"`
		MacName string `json:"mac_name"`
	}
	_ = json.NewDecoder(response.Body).Decode(&paired)
	if response.StatusCode != http.StatusOK || paired.Token == "" || paired.MacName != "Test Mac" {
		t.Fatalf("pairing failed: status %d, %+v", response.StatusCode, paired)
	}
	return paired.Token
}

func (r testRelay) connect(t *testing.T) (context.Context, *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := r.dial(t, ctx, r.pair(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return ctx, conn
}

func (r testRelay) dial(t *testing.T, ctx context.Context, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	return websocket.Dial(ctx, strings.Replace(r.url, "http", "ws", 1)+"/v1/ws", &websocket.DialOptions{HTTPHeader: header})
}

var (
	backlogMu sync.Mutex
	backlog   = map[*websocket.Conn][]map[string]any{}
)

func takeFromBacklog(conn *websocket.Conn, matches func(map[string]any) bool) map[string]any {
	backlogMu.Lock()
	defer backlogMu.Unlock()
	for index, message := range backlog[conn] {
		if matches(message) {
			backlog[conn] = append(backlog[conn][:index], backlog[conn][index+1:]...)
			return message
		}
	}
	return nil
}

func readMatching(t *testing.T, ctx context.Context, conn *websocket.Conn, want string, matches func(map[string]any) bool) map[string]any {
	t.Helper()
	if message := takeFromBacklog(conn, matches); message != nil {
		return message
	}
	for {
		var message map[string]any
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		if matches(message) {
			return message
		}
		backlogMu.Lock()
		backlog[conn] = append(backlog[conn], message)
		backlogMu.Unlock()
	}
}

func readUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, kind string) map[string]any {
	t.Helper()
	return readMatching(t, ctx, conn, kind, func(message map[string]any) bool { return message["type"] == kind })
}

func resultFor(t *testing.T, ctx context.Context, conn *websocket.Conn, id string) map[string]any {
	t.Helper()
	return readMatching(t, ctx, conn, "result "+id, func(message map[string]any) bool { return message["type"] == "result" && message["id"] == id })
}

func errorCode(result map[string]any) any {
	failure, _ := result["error"].(map[string]any)
	return failure["code"]
}

func TestPairedPhoneCanWatchAndTypeIntoATerminal(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	welcome := readUntil(t, ctx, conn, "result")
	if welcome["ok"] != true || welcome["data"].(map[string]any)["cmux_version"] != "0.65.1" {
		t.Fatalf("unexpected welcome: %v", welcome)
	}
	if unsupported, ok := welcome["data"].(map[string]any)["unsupported"].([]any); !ok || !slices.Equal(unsupported, []any{"answer_approval", "send_agent_message"}) {
		t.Fatalf("the welcome must list what this relay can't serve: %v", welcome["data"])
	}
	workspaces := readUntil(t, ctx, conn, "workspaces")
	if len(workspaces["workspaces"].([]any)) != 1 {
		t.Fatalf("unexpected workspaces: %v", workspaces)
	}
	if groups := workspaces["groups"].([]any); len(groups) != 1 || groups[0].(map[string]any)["name"] != "Q4" {
		t.Fatalf("unexpected groups: %v", workspaces["groups"])
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1", "fit": map[string]int{"columns": 40, "rows": 20}})
	frame := readUntil(t, ctx, conn, "screen")
	if frame["full"] != true || frame["surface_id"] != "t1" {
		t.Fatalf("unexpected first frame: %v", frame)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "input", "id": "2", "text": "ls\r"})
	if result := readUntil(t, ctx, conn, "result"); result["ok"] != true {
		t.Fatalf("input failed: %v", result)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "key", "id": "3", "key": "ctrl-c"})
	readUntil(t, ctx, conn, "result")
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "detach", "id": "4"})
	readUntil(t, ctx, conn, "result")

	inputs, keys, viewports := relay.cmux.snapshot()
	if len(inputs) != 1 || inputs[0] != "w1/t1:ls\r" {
		t.Fatalf("input must reach the attached terminal, got %v", inputs)
	}
	if len(keys) != 1 || keys[0] != "t1:ctrl-c" {
		t.Fatalf("keys = %v", keys)
	}
	if strings.Join(viewports, ",") != "set,clear" {
		t.Fatalf("detaching must give the Mac its size back, viewports = %v", viewports)
	}
}

func TestRelayRejectsUnknownTokensAndInputBeforeAttach(t *testing.T) {
	relay := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, response, err := relay.dial(t, ctx, "not-a-token")
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown token must get 401, got %v %v", err, response)
	}

	conn, _, err := relay.dial(t, ctx, relay.pair(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "input", "id": "1", "text": "rm -rf /\r"})
	result := readUntil(t, ctx, conn, "result")
	if result["ok"] != false || errorCode(result) != "not_attached" {
		t.Fatalf("input without an attached terminal must fail, got %v", result)
	}
	if inputs, _, _ := relay.cmux.snapshot(); len(inputs) != 0 {
		t.Fatalf("no input should reach cmux, got %v", inputs)
	}
}

func TestHistoryReturnsScrollbackOldestFirstForTheAttachedTerminal(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "history", "id": "early"})
	if early := readUntil(t, ctx, conn, "result"); early["ok"] != false {
		t.Fatalf("history before attach must fail, got %v", early)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "history", "id": "h", "lines": 999999})
	result := readUntil(t, ctx, conn, "result")
	if result["ok"] != true {
		t.Fatalf("history failed: %v", result)
	}
	var history struct {
		Styles []struct {
			ID int    `json:"id"`
			Fg string `json:"fg"`
		} `json:"styles"`
		Lines [][]struct {
			Column int    `json:"column"`
			Style  int    `json:"style"`
			Text   string `json:"text"`
		} `json:"lines"`
		Total int `json:"total"`
	}
	encoded, _ := json.Marshal(result["data"])
	if err := json.Unmarshal(encoded, &history); err != nil {
		t.Fatal(err)
	}
	if history.Total != 120 || len(history.Lines) != 3 {
		t.Fatalf("want 3 lines out of 120, got %d lines, total %d", len(history.Lines), history.Total)
	}
	if history.Lines[0][0].Text != "oldest" || len(history.Lines[1]) != 0 || history.Lines[2][1].Text != "passed" {
		t.Fatalf("lines must keep cmux's order, with blank lines kept: %+v", history.Lines)
	}
	colors := map[int]string{}
	for _, style := range history.Styles {
		colors[style.ID] = style.Fg
	}
	if colors[history.Lines[2][1].Style] != "#00FF00" || colors[history.Lines[0][0].Style] != "#FFFFFF" {
		t.Fatalf("each span's style must resolve within the response: %+v %+v", history.Styles, history.Lines)
	}

	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if len(relay.cmux.historyRequests) != 1 || relay.cmux.historyRequests[0] != fmt.Sprintf("t1:%d", maxHistoryLines) {
		t.Fatalf("history must target the attached terminal and cap the size, got %v", relay.cmux.historyRequests)
	}
}

func TestASlowRequestDoesNotHoldUpTypingAndKeepsItsTerminal(t *testing.T) {
	relay := startRelay(t)
	gate := make(chan struct{})
	relay.cmux.mu.Lock()
	relay.cmux.historyGate = gate
	relay.cmux.mu.Unlock()
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "history", "id": "h"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "input", "id": "i", "text": "ls\r"})
	if result := resultFor(t, ctx, conn, "i"); result["ok"] != true {
		t.Fatalf("input must be answered while history is still loading: %v", result)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "id": "a", "workspace_id": "w1", "surface_id": "t2"})
	resultFor(t, ctx, conn, "a")
	close(gate)
	if result := resultFor(t, ctx, conn, "h"); result["ok"] != true {
		t.Fatalf("history failed: %v", result)
	}

	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if len(relay.cmux.historyRequests) != 1 || !strings.HasPrefix(relay.cmux.historyRequests[0], "t1:") {
		t.Fatalf("history must read the terminal attached when it was asked for, got %v", relay.cmux.historyRequests)
	}
}

func TestDetachedMessageWhenTerminalDisappears(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "gone"})
	detached := readUntil(t, ctx, conn, "detached")
	if detached["surface_id"] != "gone" {
		t.Fatalf("unexpected detached message: %v", detached)
	}
}

func TestClosingTheRelayGivesFittedTerminalsTheirSizeBack(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "id": "1", "workspace_id": "w1", "surface_id": "t1", "fit": map[string]int{"columns": 40, "rows": 20}})
	if result := readUntil(t, ctx, conn, "result"); result["ok"] != true {
		t.Fatalf("attach failed: %v", result)
	}

	relay.server.Close(ctx)

	if _, _, viewports := relay.cmux.snapshot(); strings.Join(viewports, ",") != "set,clear" {
		t.Fatalf("closing the relay must give the Mac its size back, viewports = %v", viewports)
	}
}

func TestChatFollowsClaudeEnteringAndLeavingATerminal(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	transcript := filepath.Join(relay.claudeDir, "projects", "-Users-me-app", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	if started := readUntil(t, ctx, conn, "chat_session"); started["session_id"] != nil {
		t.Fatalf("a plain shell must not report a chat session: %v", started)
	}

	relay.cmux.setAgent(cmux.AgentSession{Kind: "claude", SessionID: "s1", Cwd: "/Users/me/app"})
	if started := readUntil(t, ctx, conn, "chat_session"); started["session_id"] != "s1" || started["surface_id"] != "t1" {
		t.Fatalf("entering Claude = %v", started)
	}
	first := readUntil(t, ctx, conn, "chat")
	entries := first["entries"].([]any)
	if first["reset"] != true || len(entries) != 1 || entries[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("first chat = %v", first)
	}

	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}` + "\n")
	file.Close()
	update := readUntil(t, ctx, conn, "chat")
	entries = update["entries"].([]any)
	if update["reset"] != false || len(entries) != 1 || entries[0].(map[string]any)["text"] != "hi there" {
		t.Fatalf("appended chat = %v", update)
	}

	relay.cmux.setAgent(cmux.AgentSession{})
	if ended := readUntil(t, ctx, conn, "chat_session"); ended["session_id"] != nil {
		t.Fatalf("leaving Claude = %v", ended)
	}

	relay.cmux.setRunning(claude.SessionRef{SessionID: "s1", Cwd: "/Users/me/app"})
	if found := readUntil(t, ctx, conn, "chat_session"); found["session_id"] != "s1" {
		t.Fatalf("a Claude session cmux does not track must still be found: %v", found)
	}
}

func TestChatFollowsACodexSession(t *testing.T) {
	relay := startRelay(t)
	ctx, conn := relay.connect(t)

	rollout := filepath.Join(relay.codexDir, "sessions", "2026", "10", "17", "rollout-2026-10-17T20-12-45-thread-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
		t.Fatal(err)
	}
	first := `{"timestamp":"T1","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"hello"}]}}}`
	if err := os.WriteFile(rollout, []byte(first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	relay.cmux.setAgent(cmux.AgentSession{Kind: "codex", SessionID: "thread-1", Cwd: "/Users/me/app"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	if started := readUntil(t, ctx, conn, "chat_session"); started["session_id"] != "thread-1" {
		t.Fatalf("entering Codex = %v", started)
	}
	chat := readUntil(t, ctx, conn, "chat")
	entries := chat["entries"].([]any)
	if chat["reset"] != true || len(entries) != 1 || entries[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("first chat = %v", chat)
	}

	file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(`{"timestamp":"T2","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"c1","command":["/bin/zsh","-lc","ls"],"status":"completed","aggregated_output":"a.txt","exit_code":0}}}` + "\n")
	file.Close()
	update := readUntil(t, ctx, conn, "chat")
	entries = update["entries"].([]any)
	tool, _ := entries[0].(map[string]any)["tool"].(map[string]any)
	if update["reset"] != false || len(entries) != 1 || tool["name"] != "Shell" || tool["summary"] != "ls" {
		t.Fatalf("appended chat = %v", update)
	}
}

func TestChatFindsClaudeWhenCmuxCannotReportAgents(t *testing.T) {
	relay := startRelay(t)
	relay.cmux.agentErr = &cmux.RPCError{Code: "method_not_found"}
	relay.cmux.setRunning(claude.SessionRef{SessionID: "s1", Cwd: "/Users/me/app"})
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "attach", "workspace_id": "w1", "surface_id": "t1"})
	if found := readUntil(t, ctx, conn, "chat_session"); found["session_id"] != "s1" {
		t.Fatalf("a cmux without surface.resume.get must still find Claude: %v", found)
	}
}
