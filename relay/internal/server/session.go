package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/agents"
	"github.com/gantoine/muxer/relay/internal/approvals"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/devices"
	"github.com/gantoine/muxer/relay/internal/pubsub"
	"github.com/gantoine/muxer/relay/internal/screen"
)

const (
	writeTimeout         = 10 * time.Second
	pingInterval         = 30 * time.Second
	revocationCheck      = 10 * time.Second
	maxConsecutiveErrors = 10
	maxInputBytes        = 64 * 1024
	defaultHistoryLines  = 2000
	maxHistoryLines      = 5000
	maxQueuedMessages    = 64
	maxParallelRequests  = 8
	callTimeout          = 15 * time.Second
	hostNameTimeout      = 2 * time.Second
	maxImages            = 4
	maxImageBytes        = 5 << 20
	maxMessageBytes      = 32 << 20
)

type requestError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *requestError) Error() string { return e.Code + ": " + e.Message }

func invalid(message string) error {
	return &requestError{Code: "invalid_params", Message: message}
}

type attachment struct {
	workspaceID  string
	surfaceID    string
	cancel       context.CancelFunc
	done         chan struct{}
	wake         chan struct{}
	resync       atomic.Bool
	lastActivity atomic.Int64
	fitted       bool
}

type session struct {
	server   *Server
	conn     *websocket.Conn
	device   devices.Device
	clientID string

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	attached *attachment
	started  bool

	parallelSlots chan struct{}
	parallel      sync.WaitGroup
}

var parallelRequests = map[string]bool{
	"history":         true,
	"chat_image":      true,
	"chat_files":      true,
	"recent_sessions": true,
	"resume_session":  true,
	"notifications":   true,
	"keep_awake":      true,
	"top":             true,
	"host_usage":      true,
	"vms":             true,
	"automations":     true,
	"automation_logs": true,
}

func newSession(server *Server, conn *websocket.Conn, device devices.Device) *session {
	return &session{
		server:        server,
		conn:          conn,
		device:        device,
		clientID:      "muxer-" + device.ID,
		parallelSlots: make(chan struct{}, maxParallelRequests),
	}
}

func (s *session) run() {
	s.ctx, s.cancel = context.WithCancel(s.server.ctx)
	defer s.parallel.Wait()
	defer s.cancel()
	defer s.conn.CloseNow()
	go s.keepAlive()
	go s.watchRevocation()

	for raw := range s.readMessages() {
		if s.ctx.Err() != nil {
			break
		}
		s.handle(raw)
	}
	s.detach()
}

func (s *session) readMessages() <-chan json.RawMessage {
	messages := make(chan json.RawMessage, maxQueuedMessages)
	go func() {
		defer close(messages)
		for {
			var raw json.RawMessage
			if err := wsjson.Read(s.ctx, s.conn, &raw); err != nil {
				return
			}
			select {
			case messages <- raw:
			case <-s.ctx.Done():
				return
			}
		}
	}()
	return messages
}

func (s *session) keepAlive() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
			err := s.conn.Ping(ctx)
			cancel()
			if err != nil {
				s.cancel()
				return
			}
		}
	}
}

func (s *session) watchRevocation() {
	ticker := time.NewTicker(revocationCheck)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if s.server.devices.IsRevoked(s.device.ID) {
				_ = s.conn.Close(websocket.StatusPolicyViolation, "device removed")
				s.cancel()
				return
			}
		}
	}
}

func (s *session) send(message any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		s.cancel()
		return
	}
	s.sendEncoded(encoded)
}

func (s *session) sendEncoded(encoded []byte) {
	ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, encoded); err != nil {
		s.cancel()
	}
}

type envelope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (s *session) handle(raw json.RawMessage) {
	var message envelope
	if err := json.Unmarshal(raw, &message); err != nil {
		s.reply("", nil, &requestError{Code: "invalid_message", Message: "Messages must be JSON objects with a type."})
		return
	}
	current := s.current()
	if !parallelRequests[message.Type] {
		s.answer(message, raw, current)
		return
	}
	select {
	case s.parallelSlots <- struct{}{}:
	case <-s.ctx.Done():
		return
	}
	s.parallel.Add(1)
	go func() {
		defer s.parallel.Done()
		defer func() { <-s.parallelSlots }()
		s.answer(message, raw, current)
	}()
}

func (s *session) answer(message envelope, raw json.RawMessage, current *attachment) {
	data, err := s.dispatch(message.Type, raw, current)
	if message.ID == "" && err == nil {
		return
	}
	s.reply(message.ID, data, err)
}

func (s *session) reply(id string, data any, err error) {
	result := map[string]any{"type": "result", "id": id, "ok": err == nil}
	if data != nil {
		result["data"] = data
	}
	if err != nil {
		var reqErr *requestError
		var rpcErr *cmux.RPCError
		switch {
		case errors.As(err, &reqErr):
			result["error"] = reqErr
		case errors.As(err, &rpcErr):
			result["error"] = requestError{Code: rpcErr.Code, Message: rpcErr.Message}
		default:
			result["error"] = requestError{Code: "cmux_unavailable", Message: err.Error()}
		}
	}
	s.send(result)
}

func (s *session) dispatch(kind string, raw json.RawMessage, current *attachment) (any, error) {
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	defer cancel()
	switch kind {
	case "hello":
		var hello struct {
			NotificationsAfter int64 `json:"notifications_after"`
		}
		_ = json.Unmarshal(raw, &hello)
		return s.hello(ctx, hello.NotificationsAfter)
	case "refresh":
		s.server.workspaces.refresh()
		return nil, nil
	case "attach":
		var request struct {
			WorkspaceID string `json:"workspace_id"`
			SurfaceID   string `json:"surface_id"`
			Fit         *struct {
				Columns int `json:"columns"`
				Rows    int `json:"rows"`
			} `json:"fit"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.WorkspaceID == "" || request.SurfaceID == "" {
			return nil, invalid("attach needs workspace_id and surface_id.")
		}
		s.attach(request.WorkspaceID, request.SurfaceID)
		if request.Fit != nil {
			return nil, s.fit(ctx, s.current(), request.Fit.Columns, request.Fit.Rows)
		}
		return nil, nil
	case "detach":
		s.detach()
		return nil, nil
	case "resync":
		if current := s.current(); current != nil {
			current.resync.Store(true)
			pubsub.Signal(current.wake)
		}
		return nil, nil
	case "input":
		var request struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &request)
		return nil, s.withTerminal(current, func(current *attachment) error {
			if err := validText("input", request.Text); err != nil {
				return err
			}
			return s.server.cmux.Input(ctx, current.workspaceID, current.surfaceID, request.Text)
		})
	case "key":
		var request struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(raw, &request)
		return nil, s.withTerminal(current, func(current *attachment) error {
			if request.Key == "" || len(request.Key) > 64 {
				return invalid("key needs a key name such as ctrl-c or up.")
			}
			return s.server.cmux.SendKey(ctx, current.workspaceID, current.surfaceID, request.Key)
		})
	case "paste":
		var request struct {
			Text   string `json:"text"`
			Submit bool   `json:"submit"`
		}
		_ = json.Unmarshal(raw, &request)
		return nil, s.withTerminal(current, func(current *attachment) error {
			if err := validText("paste", request.Text); err != nil {
				return err
			}
			return s.server.cmux.Paste(ctx, current.workspaceID, current.surfaceID, request.Text, request.Submit)
		})
	case "scroll":
		var request struct {
			Lines int `json:"lines"`
		}
		_ = json.Unmarshal(raw, &request)
		return nil, s.withTerminal(current, func(current *attachment) error {
			return s.server.cmux.Scroll(ctx, current.workspaceID, current.surfaceID, request.Lines)
		})
	case "history":
		var request struct {
			Lines int `json:"lines"`
		}
		_ = json.Unmarshal(raw, &request)
		if request.Lines <= 0 {
			request.Lines = defaultHistoryLines
		}
		request.Lines = min(request.Lines, maxHistoryLines)
		var history screen.History
		err := s.withTerminal(current, func(current *attachment) error {
			replay, err := s.server.cmux.History(ctx, current.workspaceID, current.surfaceID, request.Lines)
			if err != nil {
				return err
			}
			history = screen.HistoryFrom(replay.RenderGrid)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return history, nil
	case "fit":
		var request struct {
			Columns int `json:"columns"`
			Rows    int `json:"rows"`
		}
		_ = json.Unmarshal(raw, &request)
		return nil, s.fit(ctx, current, request.Columns, request.Rows)
	case "unfit":
		return nil, s.unfit(ctx)
	case "create_workspace":
		var request struct {
			Title string `json:"title"`
			Cwd   string `json:"cwd"`
		}
		_ = json.Unmarshal(raw, &request)
		created, err := s.server.cmux.CreateWorkspace(ctx, request.Title, request.Cwd)
		s.server.workspaces.refresh()
		return created, err
	case "close_workspace":
		var request struct {
			WorkspaceID string `json:"workspace_id"`
			Force       bool   `json:"force"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.WorkspaceID == "" {
			return nil, invalid("close_workspace needs workspace_id.")
		}
		err := s.server.cmux.CloseWorkspace(ctx, request.WorkspaceID, request.Force)
		s.server.workspaces.refresh()
		return nil, err
	case "rename_workspace":
		var request struct {
			WorkspaceID string `json:"workspace_id"`
			Title       string `json:"title"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.WorkspaceID == "" || strings.TrimSpace(request.Title) == "" {
			return nil, invalid("rename_workspace needs workspace_id and title.")
		}
		err := s.server.cmux.RenameWorkspace(ctx, request.WorkspaceID, request.Title)
		s.server.workspaces.refresh()
		return nil, err
	case "close_terminal":
		return nil, s.server.terminalRequest(ctx, kind, raw, s.server.closeTerminal)
	case "rename_terminal":
		return nil, s.server.terminalRequest(ctx, kind, raw, s.server.renameTerminal)
	case "restart_terminal":
		return nil, s.server.terminalRequest(ctx, kind, raw, s.server.restartTerminal)
	case "recent_sessions":
		request, err := parseSessionQuery(raw)
		if err != nil {
			return nil, err
		}
		sessions, err := s.server.recentSessions(ctx, request)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessions": sessions}, nil
	case "resume_session":
		var request struct {
			Agent     string `json:"agent"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.Agent == "" || request.SessionID == "" {
			return nil, invalid("resume_session needs agent and session_id.")
		}
		resumed, err := s.server.resumeSession(ctx, request.Agent, request.SessionID)
		if err != nil {
			return nil, err
		}
		return resumed, nil
	case "create_terminal":
		var request struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.WorkspaceID == "" {
			return nil, invalid("create_terminal needs workspace_id.")
		}
		terminalID, err := s.server.cmux.CreateTerminal(ctx, request.WorkspaceID)
		s.server.workspaces.refresh()
		return map[string]string{"surface_id": terminalID}, err
	case "answer_approval":
		var request struct {
			ApprovalID string `json:"approval_id"`
			approvals.Answer
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.ApprovalID == "" {
			return nil, invalid("answer_approval needs approval_id and either mode or answers.")
		}
		if s.server.options.Approvals == nil {
			return nil, &requestError{Code: "unsupported", Message: "This relay can't answer agent requests."}
		}
		err := s.server.options.Approvals.Answer(ctx, request.ApprovalID, request.Answer)
		if errors.Is(err, approvals.ErrNotPending) {
			return nil, &requestError{Code: "not_pending", Message: "That request was already answered or timed out."}
		}
		var invalidAnswer approvals.InvalidAnswer
		if errors.As(err, &invalidAnswer) {
			return nil, invalid(err.Error())
		}
		return nil, err
	case "notifications":
		return s.server.notifications(ctx)
	case "mark_notifications_read":
		var request markReadRequest
		_ = json.Unmarshal(raw, &request)
		return nil, s.server.markRead(ctx, request)
	case "keep_awake":
		return s.server.readKeepAwake(ctx)
	case "set_keep_awake":
		var request struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.Enabled == nil {
			return nil, invalid("set_keep_awake needs enabled.")
		}
		return s.server.setKeepAwake(ctx, *request.Enabled)
	case "top":
		return s.server.top(ctx)
	case "host_usage":
		if s.server.options.HostUsage == nil {
			return nil, &requestError{Code: "unsupported", Message: "This relay can't measure the computer."}
		}
		return s.server.options.HostUsage(ctx)
	case "vms":
		return s.server.vms(ctx)
	case "set_vm_paused":
		var request struct {
			VMID   string `json:"vm_id"`
			Paused *bool  `json:"paused"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.VMID == "" || request.Paused == nil {
			return nil, invalid("set_vm_paused needs vm_id and paused.")
		}
		return s.server.setVMPaused(ctx, request.VMID, *request.Paused)
	case "automations":
		return s.server.automations(ctx)
	case "set_automation_enabled":
		var request struct {
			RuleID  string `json:"rule_id"`
			Enabled *bool  `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.RuleID == "" || request.Enabled == nil {
			return nil, invalid("set_automation_enabled needs rule_id and enabled.")
		}
		return s.server.setAutomationEnabled(ctx, request.RuleID, *request.Enabled)
	case "automation_logs":
		var request struct {
			RuleID string `json:"rule_id"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, invalid("automation_logs takes an optional rule_id and limit.")
		}
		return s.server.automationLogs(ctx, request.RuleID, request.Limit)
	case "reload_automations":
		return s.server.reloadAutomations(ctx)
	case "send_images":
		var request struct {
			Text   string           `json:"text"`
			Images []cmux.ChatImage `json:"images"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, invalid("send_images needs images and optional text.")
		}
		if err := validImages(request.Images); err != nil {
			return nil, err
		}
		if len(request.Text) > maxInputBytes {
			return nil, invalid(fmt.Sprintf("The message can be up to %d bytes.", maxInputBytes))
		}
		return nil, s.withTerminal(current, func(current *attachment) error {
			return s.sendImages(ctx, current, request.Text, request.Images)
		})
	case "chat_image":
		var request struct {
			Image string `json:"image"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || request.Image == "" {
			return nil, invalid("chat_image needs an image.")
		}
		if strings.HasPrefix(request.Image, "/") {
			return readAttachedImage(request.Image)
		}
		var image cmux.ChatImage
		err := s.withTerminal(current, func(current *attachment) error {
			var err error
			image, err = s.transcriptImage(ctx, current, request.Image)
			return err
		})
		return image, err
	case "chat_files":
		var request struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &request); err != nil || len(request.Query) > maxFileQueryBytes {
			return nil, invalid(fmt.Sprintf("chat_files needs a query of up to %d bytes.", maxFileQueryBytes))
		}
		var found []string
		err := s.withTerminal(current, func(current *attachment) error {
			var err error
			found, err = s.chatFiles(ctx, current, request.Query)
			return err
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"files": found}, nil
	case "send_agent_message":
		var request struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &request)
		if s.server.options.Agents == nil {
			return nil, &requestError{Code: "unsupported", Message: "This relay can't message agents."}
		}
		var sent cmux.AgentMessage
		err := s.withTerminal(current, func(current *attachment) error {
			var err error
			sent, err = guarded(s.server, cmux.MethodAgentMessageSend, errAgentMessagesUnsupported, func() (cmux.AgentMessage, error) {
				return s.server.options.Agents.Send(ctx, current.surfaceID, s.device.Name, request.Text)
			})
			return err
		})
		if errors.Is(err, agents.ErrInvalidMessage) {
			return nil, invalid("Write a message of up to 32 KB.")
		}
		if err != nil {
			return nil, err
		}
		return sent, nil
	default:
		return s.organizingRequest(ctx, kind, raw)
	}
}

var errAgentMessagesUnsupported = unsupported("message agents")

func unknownType(kind string) error {
	return &requestError{Code: "unknown_type", Message: fmt.Sprintf("Unknown message type %q.", kind)}
}

func validText(kind, text string) error {
	if text == "" || len(text) > maxInputBytes {
		return invalid(fmt.Sprintf("%s needs 1 to %d bytes of text.", kind, maxInputBytes))
	}
	return nil
}

func (s *session) hello(ctx context.Context, notificationsAfter int64) (any, error) {
	identity, err := s.server.cmux.Identify(ctx)
	if err != nil {
		return nil, err
	}
	s.server.methods.load(ctx, s.server.cmux)
	s.mu.Lock()
	alreadyStarted := s.started
	s.started = true
	s.mu.Unlock()
	welcome := map[string]any{
		"mac_name":         s.server.macName(ctx),
		"relay_version":    s.server.options.RelayVersion,
		"protocol_version": ProtocolVersion,
		"cmux_version":     identity.Version,
		"device_id":        s.device.ID,
		"unsupported":      s.server.unsupportedRequests(),
	}
	if s.server.hub != nil {
		latest := s.server.hub.LatestSeq()
		welcome["notification_seq"] = latest
		if notificationsAfter <= 0 {
			notificationsAfter = latest
		}
	}
	if !alreadyStarted {
		go s.follow(s.server.workspaces)
		go s.forwardNotifications(notificationsAfter)
		go s.forwardApprovals()
		go s.forwardAgentActivity()
		go s.follow(s.server.keepAwake)
	}
	return welcome, nil
}

func (s *session) forwardNotifications(after int64) {
	if s.server.hub == nil {
		return
	}
	updates, missed, unsubscribe := s.server.hub.Subscribe(after)
	defer unsubscribe()
	for _, notification := range missed {
		s.send(map[string]any{"type": "notification", "notification": notification})
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case notification := <-updates:
			s.send(map[string]any{"type": "notification", "notification": notification})
		}
	}
}

func onEachChange(ctx context.Context, changes <-chan struct{}, push func()) {
	for {
		push()
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
	}
}

func (s *session) forwardApprovals() {
	tracker := s.server.options.Approvals
	if tracker == nil {
		return
	}
	changes, unsubscribe := tracker.Subscribe()
	defer unsubscribe()
	onEachChange(s.ctx, changes, func() {
		pending := tracker.Pending()
		if pending == nil {
			pending = []approvals.Approval{}
		}
		s.send(map[string]any{"type": "approvals", "approvals": pending})
	})
}

func (s *session) forwardAgentActivity() {
	tracker := s.server.options.Agents
	if tracker == nil {
		return
	}
	changes, unsubscribe := tracker.SubscribeActivity()
	defer unsubscribe()
	onEachChange(s.ctx, changes, func() {
		s.send(map[string]any{"type": "agent_activity", "retries": tracker.Retries(), "busy": tracker.Busy()})
	})
}

func (s *session) watchAgentMessages(ctx context.Context, current *attachment) {
	tracker := s.server.options.Agents
	if tracker == nil {
		return
	}
	changes, unsubscribe := tracker.SubscribeMessages(current.surfaceID)
	defer unsubscribe()
	onEachChange(ctx, changes, func() {
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		messages, err := tracker.Messages(callCtx, current.surfaceID)
		cancel()
		switch {
		case ctx.Err() != nil:
		case err != nil:
			s.server.logger.Warn("could not list agent messages", "error", err)
		default:
			if messages == nil {
				messages = []cmux.AgentMessage{}
			}
			s.send(map[string]any{"type": "agent_messages", "surface_id": current.surfaceID, "messages": messages})
		}
	})
}

func (s *session) current() *attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attached
}

func (s *session) withTerminal(current *attachment, action func(*attachment) error) error {
	if current == nil {
		return &requestError{Code: "not_attached", Message: "Open a terminal first."}
	}
	current.lastActivity.Store(time.Now().UnixNano())
	err := action(current)
	pubsub.Signal(current.wake)
	return err
}

func (s *session) attach(workspaceID, surfaceID string) {
	s.detach()
	ctx, cancel := context.WithCancel(s.ctx)
	current := &attachment{
		workspaceID: workspaceID,
		surfaceID:   surfaceID,
		cancel:      cancel,
		done:        make(chan struct{}),
		wake:        make(chan struct{}, 1),
	}
	current.lastActivity.Store(time.Now().UnixNano())
	s.mu.Lock()
	s.attached = current
	s.mu.Unlock()
	go s.pollTerminal(ctx, current)
	go s.watchChat(ctx, current)
	go s.watchAgentMessages(ctx, current)
	go s.watchInputState(ctx, current)
	go s.markAttachedRead(ctx, surfaceID)
}

func (s *session) detach() {
	s.mu.Lock()
	current := s.attached
	s.attached = nil
	fitted := current != nil && current.fitted
	s.mu.Unlock()
	if current == nil {
		return
	}
	current.cancel()
	<-current.done
	if fitted {
		s.clearViewport(current)
	}
}

func (s *session) fit(ctx context.Context, current *attachment, columns, rows int) error {
	if columns < 20 || columns > 300 || rows < 5 || rows > 120 {
		return invalid("fit needs 20-300 columns and 5-120 rows.")
	}
	return s.withTerminal(current, func(current *attachment) error {
		err := s.server.cmux.SetViewport(ctx, current.workspaceID, current.surfaceID, cmux.Viewport{
			ClientID: s.clientID, Columns: columns, Rows: rows, Generation: s.server.nextViewportGeneration(),
		})
		if err == nil {
			s.mu.Lock()
			current.fitted = true
			s.mu.Unlock()
		}
		return err
	})
}

func (s *session) unfit(ctx context.Context) error {
	s.mu.Lock()
	current := s.attached
	if current == nil || !current.fitted {
		s.mu.Unlock()
		return nil
	}
	current.fitted = false
	s.mu.Unlock()
	return s.clearViewportWith(ctx, current)
}

func (s *session) clearViewport(current *attachment) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.clearViewportWith(ctx, current); err != nil {
		s.server.logger.Warn("could not restore the computer's terminal size", "error", err)
	}
}

func (s *session) clearViewportWith(ctx context.Context, current *attachment) error {
	err := s.server.cmux.ClearViewport(ctx, current.workspaceID, current.surfaceID, s.clientID, s.server.nextViewportGeneration())
	pubsub.Signal(current.wake)
	return err
}

func (s *session) pollTerminal(ctx context.Context, current *attachment) {
	defer close(current.done)
	tracker := screen.NewTracker()
	intervals := s.server.options.PollInterval
	lastChange := time.Now()
	consecutiveErrors := 0
	for {
		if current.resync.Swap(false) {
			tracker.Reset()
		}
		replay, err := s.server.cmux.Replay(ctx, current.workspaceID, current.surfaceID)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			consecutiveErrors++
			if cmux.IsNotFound(err) || consecutiveErrors >= maxConsecutiveErrors {
				s.send(map[string]any{"type": "detached", "surface_id": current.surfaceID, "reason": err.Error()})
				current.cancel()
				return
			}
		} else {
			consecutiveErrors = 0
			if frame, changed := tracker.Update(replay); changed {
				s.send(frame)
				lastChange = time.Now()
			}
		}

		quietFor := time.Since(lastChange)
		if sinceActivity := time.Since(time.Unix(0, current.lastActivity.Load())); sinceActivity < quietFor {
			quietFor = sinceActivity
		}
		interval := intervals.Fast
		switch {
		case err != nil:
			interval = intervals.Idle * time.Duration(consecutiveErrors)
		case quietFor > intervals.SlowAfter:
			interval = intervals.Slow
		case quietFor > intervals.IdleAfter:
			interval = intervals.Idle
		}
		select {
		case <-ctx.Done():
			return
		case <-current.wake:
		case <-time.After(interval):
		}
	}
}
