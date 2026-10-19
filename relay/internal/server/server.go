package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/gantoine/muxer/relay/internal/agents"
	"github.com/gantoine/muxer/relay/internal/approvals"
	"github.com/gantoine/muxer/relay/internal/claude"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/codex"
	"github.com/gantoine/muxer/relay/internal/devices"
	"github.com/gantoine/muxer/relay/internal/files"
	"github.com/gantoine/muxer/relay/internal/host"
	"github.com/gantoine/muxer/relay/internal/notify"
)

const ProtocolVersion = 1

const claudeScanMaxAge = 2 * time.Second

type Intervals struct {
	Fast       time.Duration
	Idle       time.Duration
	Slow       time.Duration
	IdleAfter  time.Duration
	SlowAfter  time.Duration
	Workspaces time.Duration
	Chat       time.Duration
	KeepAwake  time.Duration
	Git        time.Duration
	InputState time.Duration
	Organizing time.Duration
}

var DefaultIntervals = Intervals{
	Fast:       80 * time.Millisecond,
	Idle:       250 * time.Millisecond,
	Slow:       time.Second,
	IdleAfter:  3 * time.Second,
	SlowAfter:  time.Minute,
	Workspaces: 2 * time.Second,
	Chat:       500 * time.Millisecond,
	KeepAwake:  30 * time.Second,
	Git:        10 * time.Second,
	InputState: time.Second,
	Organizing: 5 * time.Second,
}

type Cmux interface {
	Identify(ctx context.Context) (cmux.Identity, error)
	Workspaces(ctx context.Context) (cmux.WorkspaceList, error)
	Replay(ctx context.Context, workspaceID, surfaceID string) (cmux.Replay, error)
	History(ctx context.Context, workspaceID, surfaceID string, maxRows int) (cmux.Replay, error)
	Input(ctx context.Context, workspaceID, surfaceID, text string) error
	SendKey(ctx context.Context, workspaceID, surfaceID, key string) error
	Paste(ctx context.Context, workspaceID, surfaceID, text string, submit bool) error
	Scroll(ctx context.Context, workspaceID, surfaceID string, deltaLines int) error
	SetViewport(ctx context.Context, workspaceID, surfaceID string, viewport cmux.Viewport) error
	ClearViewport(ctx context.Context, workspaceID, surfaceID, clientID string, generation int) error
	CreateWorkspace(ctx context.Context, title, cwd string) (cmux.CreatedWorkspace, error)
	CloseWorkspace(ctx context.Context, workspaceID string, force bool) error
	RenameWorkspace(ctx context.Context, workspaceID, title string) error
	CreateTerminal(ctx context.Context, workspaceID string) (string, error)
	CloseTerminal(ctx context.Context, workspaceID, surfaceID string, force bool) error
	RenameTerminal(ctx context.Context, workspaceID, surfaceID, title string) error
	RestartTerminal(ctx context.Context, workspaceID, surfaceID, command string) error
	InputState(ctx context.Context, workspaceID, surfaceID string) (cmux.InputState, error)
	RunCommand(ctx context.Context, workspaceID, surfaceID, command string) (bool, error)
	VaultSessions(ctx context.Context, agent string, limit int) ([]cmux.VaultSession, error)
	VaultSearch(ctx context.Context, query string, limit int) ([]cmux.VaultSession, error)
	Call(ctx context.Context, method string, params any, result any) error
	LaneStatus(ctx context.Context, workspaceID string) (cmux.LaneStatus, error)
	Checklist(ctx context.Context, workspaceID string) (cmux.Checklist, error)
	GroupDetails(ctx context.Context) ([]cmux.GroupDetails, error)
	RemoteState(ctx context.Context, workspaceID string) (*cmux.RemoteState, error)
	AgentSession(ctx context.Context, workspaceID, surfaceID string) (cmux.AgentSession, error)
	KeepAwake(ctx context.Context) (bool, error)
	TerminalGit(ctx context.Context) ([]cmux.TerminalGit, error)
	SetKeepAwake(ctx context.Context, enabled bool) (bool, error)
	Top(ctx context.Context) (cmux.Top, error)
	VMs(ctx context.Context) (cmux.VMList, error)
	VMStatus(ctx context.Context, id string) (cmux.VM, error)
	VMStats(ctx context.Context, id string) (cmux.VMStats, error)
	SetVMPaused(ctx context.Context, id string, paused bool) error
	Methods(ctx context.Context) ([]string, error)
	HostName(ctx context.Context) (string, error)
	SendChat(ctx context.Context, sessionID, text string, images []cmux.ChatImage) error
	Notifications(ctx context.Context) ([]cmux.Notification, error)
	MarkNotificationRead(ctx context.Context, id string) error
	MarkAllNotificationsRead(ctx context.Context) error
	MarkSurfaceNotificationsRead(ctx context.Context, surfaceID string) error
}

type Approvals interface {
	Pending() []approvals.Approval
	Subscribe() (<-chan struct{}, func())
	Answer(ctx context.Context, id string, answer approvals.Answer) error
}

type Agents interface {
	Retries() []agents.Retry
	Busy() []string
	SubscribeActivity() (<-chan struct{}, func())
	SubscribeMessages(surfaceID string) (<-chan struct{}, func())
	Send(ctx context.Context, surfaceID, deviceName, body string) (cmux.AgentMessage, error)
	Messages(ctx context.Context, surfaceID string) ([]cmux.AgentMessage, error)
}

type Options struct {
	MacName            string
	FallbackMacName    string
	RelayVersion       string
	PollInterval       Intervals
	ClaudeConfigDir    string
	ClaudeSession      func(surfaceID string) (claude.SessionRef, bool)
	CodexHome          string
	Approvals          Approvals
	Agents             Agents
	HostUsage          func(context.Context) (host.Usage, error)
	Tailnet            Tailnet
	Listeners          func(ctx context.Context, port int) ([]netip.Addr, error)
	ListeningProcesses func(ctx context.Context) (map[int][]int, error)
	ParentProcesses    func(ctx context.Context) (map[int]int, error)
	ExposedPorts       string
	RelayPort          int
}

type Server struct {
	cmux    Cmux
	devices *devices.Store
	hub     *notify.Hub
	options Options
	logger  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	closed   bool
	sessions sync.WaitGroup

	viewportGeneration atomic.Int64

	methods    methodSupport
	workspaces *feed
	keepAwake  *feed
	organizing *workspaceExtras
	files      *files.Index
	exposures  *portExposures
}

func New(cmuxAPI Cmux, store *devices.Store, hub *notify.Hub, options Options, logger *slog.Logger) *Server {
	if options.PollInterval == (Intervals{}) {
		options.PollInterval = DefaultIntervals
	}
	if options.ClaudeConfigDir == "" {
		options.ClaudeConfigDir = claude.ConfigDir()
	}
	if options.CodexHome == "" {
		options.CodexHome = codex.HomeDir()
	}
	if options.ClaudeSession == nil {
		options.ClaudeSession = claude.NewResolver(options.ClaudeConfigDir, claude.SystemProcesses{}, claudeScanMaxAge).ForSurface
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{cmux: cmuxAPI, devices: store, hub: hub, options: options, logger: logger, ctx: ctx, cancel: cancel}
	server.viewportGeneration.Store(time.Now().UnixMilli())
	server.organizing = &workspaceExtras{}
	server.files = files.NewIndex()
	server.exposures = &portExposures{path: options.ExposedPorts}
	server.workspaces = newFeed(options.PollInterval.Workspaces, server.pollWorkspaces(&gitInfo{}))
	server.keepAwake = newFeed(options.PollInterval.KeepAwake, server.pollKeepAwake)
	return server
}

func (s *Server) nextViewportGeneration() int {
	return int(s.viewportGeneration.Add(1))
}

func (s *Server) Close(ctx context.Context) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	drained := make(chan struct{})
	go func() {
		s.sessions.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-ctx.Done():
	}
}

func (s *Server) trackSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.sessions.Add(1)
	return true
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/pair", s.handlePair)
	mux.HandleFunc("GET /v1/ws", s.handleWebSocket)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app":              "muxer",
		"mac_name":         s.macName(r.Context()),
		"relay_version":    s.options.RelayVersion,
		"protocol_version": ProtocolVersion,
	})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid_request", "Send a JSON body with code and device_name."))
		return
	}
	device, token, err := s.devices.Pair(request.Code, request.DeviceName)
	if errors.Is(err, devices.ErrInvalidPairingCode) {
		time.Sleep(500 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, errorBody("invalid_code", "That pairing code is wrong or expired. Run `muxer pair` on your computer for a new one."))
		return
	}
	if err != nil {
		s.logger.Error("pairing failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("internal", "Pairing failed on the computer. Check the relay log."))
		return
	}
	s.logger.Info("device paired", "device_id", device.ID, "name", device.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":            token,
		"device_id":        device.ID,
		"mac_name":         s.macName(r.Context()),
		"protocol_version": ProtocolVersion,
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		token = ""
	}
	device, ok := s.devices.Authenticate(token)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "This device is not paired with the computer, or it was removed."))
		return
	}
	if !s.trackSession() {
		writeJSON(w, http.StatusServiceUnavailable, errorBody("shutting_down", "The relay is restarting. Try again in a moment."))
		return
	}
	defer s.sessions.Done()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxMessageBytes)
	session := newSession(s, conn, device)
	session.run()
}

func (s *Server) macName(ctx context.Context) string {
	if s.options.MacName != "" {
		return s.options.MacName
	}
	callCtx, cancel := context.WithTimeout(ctx, hostNameTimeout)
	defer cancel()
	name, ok := probe(s, cmux.MethodHostStatus, func() (string, error) { return s.cmux.HostName(callCtx) })
	if ok && name != "" {
		return name
	}
	return s.options.FallbackMacName
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errorBody(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}
