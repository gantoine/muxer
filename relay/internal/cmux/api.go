package cmux

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type Terminal struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	CurrentDirectory *string `json:"current_directory"`
	IsReady          bool    `json:"is_ready"`
	IsFocused        bool    `json:"is_focused"`
	AgentSource      *string `json:"agent_source"`
	AgentState       *string `json:"agent_state"`
	GitBranch        *string `json:"git_branch,omitempty"`
	GitDirty         *bool   `json:"git_dirty,omitempty"`
	ListeningPorts   []int   `json:"listening_ports,omitempty"`
}

type Workspace struct {
	ID               string             `json:"id"`
	WindowID         *string            `json:"window_id"`
	Title            string             `json:"title"`
	Description      *string            `json:"description"`
	CustomColor      *string            `json:"custom_color"`
	CurrentDirectory *string            `json:"current_directory"`
	IsSelected       bool               `json:"is_selected"`
	IsPinned         bool               `json:"is_pinned"`
	GroupID          *string            `json:"group_id"`
	Preview          *string            `json:"preview"`
	PreviewAt        *float64           `json:"preview_at"`
	LastActivityAt   *float64           `json:"last_activity_at"`
	HasUnread        bool               `json:"has_unread"`
	UnreadCount      int                `json:"unread_count"`
	Terminals        []Terminal         `json:"terminals"`
	Status           *LaneStatus        `json:"status,omitempty"`
	Checklist        *ChecklistProgress `json:"checklist,omitempty"`
	Remote           *RemoteState       `json:"remote,omitempty"`
}

type TerminalGit struct {
	SurfaceID      string  `json:"surface_id"`
	GitBranch      *string `json:"git_branch"`
	GitDirty       *bool   `json:"git_dirty"`
	ListeningPorts []int   `json:"listening_ports"`
}

type WorkspaceGroup struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	AnchorWorkspaceID  *string  `json:"anchor_workspace_id"`
	MemberWorkspaceIDs []string `json:"member_workspace_ids"`
	IsCollapsed        bool     `json:"is_collapsed"`
	IsPinned           bool     `json:"is_pinned"`
	CustomColor        *string  `json:"custom_color,omitempty"`
}

type WorkspaceList struct {
	Workspaces []Workspace      `json:"workspaces"`
	Groups     []WorkspaceGroup `json:"groups"`
}

type Cursor struct {
	Row     int    `json:"row"`
	Column  int    `json:"column"`
	Visible bool   `json:"visible"`
	Style   string `json:"style"`
}

type Style struct {
	ID            int    `json:"id"`
	Foreground    string `json:"foreground"`
	Background    string `json:"background"`
	Bold          bool   `json:"bold"`
	Faint         bool   `json:"faint"`
	Italic        bool   `json:"italic"`
	Underline     bool   `json:"underline"`
	Strikethrough bool   `json:"strikethrough"`
	Inverse       bool   `json:"inverse"`
	Invisible     bool   `json:"invisible"`
}

type Span struct {
	Row       int    `json:"row"`
	Column    int    `json:"column"`
	StyleID   int    `json:"style_id"`
	Text      string `json:"text"`
	CellWidth *int   `json:"cell_width"`
}

type RenderGrid struct {
	Format             string  `json:"format"`
	Columns            int     `json:"columns"`
	Rows               int     `json:"rows"`
	Cursor             *Cursor `json:"cursor"`
	Styles             []Style `json:"styles"`
	RowSpans           []Span  `json:"row_spans"`
	ActiveScreen       string  `json:"active_screen"`
	TerminalForeground string  `json:"terminal_foreground"`
	TerminalBackground string  `json:"terminal_background"`
	ScrollbackRows     int     `json:"scrollback_rows"`
	ScrollbackSpans    []Span  `json:"scrollback_spans"`
	HistoryRows        int     `json:"history_rows"`
}

type Replay struct {
	WorkspaceID string      `json:"workspace_id"`
	SurfaceID   string      `json:"surface_id"`
	Columns     int         `json:"columns"`
	Rows        int         `json:"rows"`
	RenderGrid  *RenderGrid `json:"render_grid"`
}

type Notification struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Body        string `json:"body"`
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
	TabTitle    string `json:"tab_title"`
	CreatedAt   string `json:"created_at"`
	IsRead      bool   `json:"is_read"`
}

type Identity struct {
	Version string `json:"version"`
}

type CreatedWorkspace struct {
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
}

type AgentSession struct {
	Kind      string
	SessionID string
	Cwd       string
}

type Viewport struct {
	ClientID   string
	Columns    int
	Rows       int
	Generation int
}

const (
	MethodIdentify         = "system.identify"
	MethodCapabilities     = "system.capabilities"
	MethodWorkspaceList    = "mobile.workspace.list"
	MethodCreateWorkspace  = "workspace.create"
	MethodCreateSurface    = "surface.create"
	MethodReplay           = "mobile.terminal.replay"
	MethodPaste            = "mobile.terminal.paste"
	MethodViewport         = "mobile.terminal.viewport"
	MethodNotificationList = "notification.list"
	MethodMarkNotification = "notification.mark_read"
	MethodFeedList         = "feed.list"
	MethodAgentMessageList = "agent.message.list"
	MethodAgentMessageSend = "agent.message.send"
	MethodAgentSession     = "surface.resume.get"
	MethodTerminalGit      = "debug.terminals"
	MethodKeepAwake        = "caffeine.status"
	MethodSetKeepAwake     = "caffeine.set"
	MethodHostStatus       = "mobile.host.status"
	MethodChatSend         = "mobile.chat.send"
	MethodCloseTerminal    = "surface.close"
	MethodRenameTerminal   = "surface.action"
	MethodRestartTerminal  = "surface.respawn"
	MethodInputState       = "surface.input_state"
	MethodTop              = "system.top"
	MethodVaultSessions    = "vault.sessions"
	MethodVaultSearch      = "vault.search"
	MethodWorkspaceAction  = "workspace.action"
	MethodLaneStatus       = "workspace.status.get"
	MethodSetLaneStatus    = "workspace.status.set"
	MethodChecklist        = "workspace.todo.list"
	MethodChecklistAdd     = "workspace.todo.add"
	MethodChecklistState   = "workspace.todo.set_state"
	MethodChecklistEdit    = "workspace.todo.edit"
	MethodChecklistRemove  = "workspace.todo.remove"
	MethodChecklistClear   = "workspace.todo.clear"
	MethodGroupList        = "workspace.group.list"
	MethodGroupCreate      = "workspace.group.create"
	MethodGroupRename      = "workspace.group.rename"
	MethodGroupPin         = "workspace.group.pin"
	MethodGroupUnpin       = "workspace.group.unpin"
	MethodGroupSetColor    = "workspace.group.set_color"
	MethodGroupAdd         = "workspace.group.add"
	MethodGroupRemove      = "workspace.group.remove"
	MethodGroupUngroup     = "workspace.group.ungroup"
	MethodGroupNewSpace    = "workspace.group.new_workspace"
	MethodRemoteStatus     = "workspace.remote.status"
	MethodRemoteReconnect  = "workspace.remote.reconnect"
)

type ChatImage struct {
	Data   string `json:"data_b64"`
	Format string `json:"format"`
}

type Capabilities struct {
	Methods    []string `json:"methods"`
	AccessMode string   `json:"access_mode"`
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var capabilities Capabilities
	err := c.Call(ctx, MethodCapabilities, nil, &capabilities)
	return capabilities, err
}

func (c *Client) Methods(ctx context.Context) ([]string, error) {
	capabilities, err := c.Capabilities(ctx)
	return capabilities.Methods, err
}

func (c *Client) Identify(ctx context.Context) (Identity, error) {
	var identity Identity
	err := c.Call(ctx, MethodIdentify, nil, &identity)
	return identity, err
}

func (c *Client) HostName(ctx context.Context) (string, error) {
	var status struct {
		MacDisplayName string `json:"mac_display_name"`
	}
	err := c.Call(ctx, MethodHostStatus, nil, &status)
	return strings.TrimSpace(status.MacDisplayName), err
}

func (c *Client) SendChat(ctx context.Context, sessionID, text string, images []ChatImage) error {
	return c.Call(ctx, MethodChatSend, map[string]any{
		"session_id":  sessionID,
		"text":        text,
		"attachments": images,
	}, nil)
}

func (c *Client) AgentSession(ctx context.Context, workspaceID, surfaceID string) (AgentSession, error) {
	var result struct {
		ResumeBinding *struct {
			Kind         string `json:"kind"`
			CheckpointID string `json:"checkpoint_id"`
			Cwd          string `json:"cwd"`
		} `json:"resume_binding"`
	}
	err := c.Call(ctx, MethodAgentSession, map[string]any{"workspace_id": workspaceID, "surface_id": surfaceID}, &result)
	if err != nil || result.ResumeBinding == nil {
		return AgentSession{}, err
	}
	binding := result.ResumeBinding
	return AgentSession{Kind: binding.Kind, SessionID: binding.CheckpointID, Cwd: binding.Cwd}, nil
}

func (c *Client) Workspaces(ctx context.Context) (WorkspaceList, error) {
	var list WorkspaceList
	err := c.Call(ctx, MethodWorkspaceList, nil, &list)
	if list.Workspaces == nil {
		list.Workspaces = []Workspace{}
	}
	if list.Groups == nil {
		list.Groups = []WorkspaceGroup{}
	}
	return list, err
}

func (c *Client) TerminalGit(ctx context.Context) ([]TerminalGit, error) {
	var dump struct {
		Terminals []TerminalGit `json:"terminals"`
	}
	err := c.Call(ctx, MethodTerminalGit, nil, &dump)
	return dump.Terminals, err
}

func (c *Client) Replay(ctx context.Context, workspaceID, surfaceID string) (Replay, error) {
	return c.replay(ctx, workspaceID, surfaceID, map[string]any{}, "cmux returned no screen for this terminal.")
}

func (c *Client) History(ctx context.Context, workspaceID, surfaceID string, maxRows int) (Replay, error) {
	return c.replay(ctx, workspaceID, surfaceID, map[string]any{
		"anchor":              "screen",
		"max_scrollback_rows": maxRows,
	}, "cmux returned no history for this terminal.")
}

func (c *Client) replay(ctx context.Context, workspaceID, surfaceID string, params map[string]any, missing string) (Replay, error) {
	params["workspace_id"], params["surface_id"] = workspaceID, surfaceID
	var replay Replay
	if err := c.Call(ctx, MethodReplay, params, &replay); err != nil {
		return Replay{}, err
	}
	if replay.RenderGrid == nil {
		return Replay{}, &RPCError{Code: "no_render_grid", Message: missing}
	}
	replay.WorkspaceID = cmp.Or(replay.WorkspaceID, workspaceID)
	replay.SurfaceID = cmp.Or(replay.SurfaceID, surfaceID)
	return replay, nil
}

func (c *Client) Input(ctx context.Context, workspaceID, surfaceID, text string) error {
	return c.Call(ctx, "mobile.terminal.input", map[string]any{
		"workspace_id": workspaceID,
		"surface_id":   surfaceID,
		"text":         text,
	}, nil)
}

func (c *Client) SendKey(ctx context.Context, workspaceID, surfaceID, key string) error {
	return c.Call(ctx, "surface.send_key", map[string]any{
		"workspace_id": workspaceID,
		"surface_id":   surfaceID,
		"key":          key,
	}, nil)
}

func (c *Client) Paste(ctx context.Context, workspaceID, surfaceID, text string, submit bool) error {
	_, err := c.paste(ctx, workspaceID, surfaceID, text, submit)
	return err
}

func (c *Client) paste(ctx context.Context, workspaceID, surfaceID, text string, submit bool) (bool, error) {
	submitKey := "none"
	if submit {
		submitKey = "return"
	}
	var result struct {
		Submitted bool `json:"submitted"`
	}
	err := c.Call(ctx, MethodPaste, map[string]any{
		"workspace_id": workspaceID,
		"surface_id":   surfaceID,
		"text":         text,
		"submit_key":   submitKey,
	}, &result)
	return result.Submitted, err
}

func (c *Client) Scroll(ctx context.Context, workspaceID, surfaceID string, deltaLines int) error {
	return c.Call(ctx, "mobile.terminal.scroll", map[string]any{
		"workspace_id": workspaceID,
		"surface_id":   surfaceID,
		"delta_lines":  deltaLines,
	}, nil)
}

func (c *Client) SetViewport(ctx context.Context, workspaceID, surfaceID string, viewport Viewport) error {
	return c.Call(ctx, MethodViewport, map[string]any{
		"workspace_id":        workspaceID,
		"surface_id":          surfaceID,
		"client_id":           viewport.ClientID,
		"viewport_columns":    viewport.Columns,
		"viewport_rows":       viewport.Rows,
		"viewport_generation": viewport.Generation,
	}, nil)
}

func (c *Client) ClearViewport(ctx context.Context, workspaceID, surfaceID, clientID string, generation int) error {
	return c.Call(ctx, MethodViewport, map[string]any{
		"workspace_id":        workspaceID,
		"surface_id":          surfaceID,
		"client_id":           clientID,
		"viewport_generation": generation,
		"clear":               true,
	}, nil)
}

func (c *Client) CreateWorkspace(ctx context.Context, title, cwd string) (CreatedWorkspace, error) {
	params := map[string]any{"focus": false}
	if title != "" {
		params["title"] = title
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	var created CreatedWorkspace
	err := c.Call(ctx, MethodCreateWorkspace, params, &created)
	return created, err
}

func (c *Client) CreateWorkspaceWithEnv(ctx context.Context, title, cwd string, env map[string]string) (CreatedWorkspace, error) {
	params := map[string]any{"focus": false, "cwd": cwd, "initial_env": env}
	if title != "" {
		params["title"] = title
	}
	var created CreatedWorkspace
	err := c.Call(ctx, MethodCreateWorkspace, params, &created)
	return created, err
}

func (c *Client) CloseWorkspace(ctx context.Context, workspaceID string, force bool) error {
	return c.Call(ctx, "workspace.close", map[string]any{"workspace_id": workspaceID, "force": force}, nil)
}

func (c *Client) RenameWorkspace(ctx context.Context, workspaceID, title string) error {
	return c.Call(ctx, "workspace.rename", map[string]any{"workspace_id": workspaceID, "title": title}, nil)
}

func (c *Client) CreateTerminal(ctx context.Context, workspaceID string) (string, error) {
	var created struct {
		CreatedTerminalID string `json:"created_terminal_id"`
	}
	err := c.Call(ctx, "mobile.terminal.create", map[string]any{"workspace_id": workspaceID}, &created)
	return created.CreatedTerminalID, err
}

func (c *Client) CreateTerminalWithEnv(ctx context.Context, workspaceID, cwd string, env map[string]string) (string, error) {
	params := map[string]any{"workspace_id": workspaceID, "type": "terminal", "focus": false, "initial_env": env}
	if cwd != "" {
		params["working_directory"] = cwd
	}
	var created struct {
		SurfaceID string `json:"surface_id"`
	}
	err := c.Call(ctx, MethodCreateSurface, params, &created)
	return created.SurfaceID, err
}

func (c *Client) RunCommand(ctx context.Context, workspaceID, surfaceID, command string) (bool, error) {
	return c.paste(ctx, workspaceID, surfaceID, command, true)
}

type VaultSession struct {
	Agent         string `json:"agent"`
	SessionID     string `json:"session_id"`
	Title         string `json:"title"`
	Cwd           string `json:"cwd"`
	GitBranch     string `json:"git_branch"`
	Created       string `json:"created"`
	Modified      string `json:"modified"`
	MessageCount  *int   `json:"message_count"`
	ResumeCommand string `json:"resume_command"`
}

type vaultList struct {
	Sessions []VaultSession `json:"sessions"`
}

func (c *Client) VaultSessions(ctx context.Context, agent string, limit int) ([]VaultSession, error) {
	params := map[string]any{"limit": limit}
	if agent != "" {
		params["agent"] = agent
	}
	var list vaultList
	err := c.Call(ctx, MethodVaultSessions, params, &list)
	return list.Sessions, err
}

func (c *Client) VaultSearch(ctx context.Context, query string, limit int) ([]VaultSession, error) {
	var list vaultList
	err := c.Call(ctx, MethodVaultSearch, map[string]any{"query": query, "limit": limit}, &list)
	return list.Sessions, err
}

type LaneStatus struct {
	Effective string `json:"effective"`
	Inferred  string `json:"inferred"`
	Override  bool   `json:"override"`
}

func (c *Client) LaneStatus(ctx context.Context, workspaceID string) (LaneStatus, error) {
	var result struct {
		Effective string          `json:"effective"`
		Inferred  string          `json:"inferred"`
		Override  json.RawMessage `json:"override"`
	}
	err := c.Call(ctx, MethodLaneStatus, map[string]any{"workspace_id": workspaceID}, &result)
	overridden := len(result.Override) > 0 && string(result.Override) != "null"
	return LaneStatus{Effective: result.Effective, Inferred: result.Inferred, Override: overridden}, err
}

type ChecklistItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	State  string `json:"state"`
	Origin string `json:"origin"`
}

type ChecklistProgress struct {
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Next      string `json:"next,omitempty"`
}

type Checklist struct {
	Items    []ChecklistItem   `json:"items"`
	Progress ChecklistProgress `json:"progress"`
}

func (c *Client) Checklist(ctx context.Context, workspaceID string) (Checklist, error) {
	var result struct {
		Items    []ChecklistItem `json:"items"`
		Progress struct {
			Completed int     `json:"completed"`
			Total     int     `json:"total"`
			Next      *string `json:"first_unchecked_text"`
		} `json:"progress"`
	}
	err := c.Call(ctx, MethodChecklist, map[string]any{"workspace_id": workspaceID}, &result)
	checklist := Checklist{Items: result.Items, Progress: ChecklistProgress{Completed: result.Progress.Completed, Total: result.Progress.Total}}
	if result.Progress.Next != nil {
		checklist.Progress.Next = *result.Progress.Next
	}
	if checklist.Items == nil {
		checklist.Items = []ChecklistItem{}
	}
	return checklist, err
}

type GroupDetails struct {
	ID          string  `json:"id"`
	CustomColor *string `json:"custom_color"`
}

func (c *Client) GroupDetails(ctx context.Context) ([]GroupDetails, error) {
	var result struct {
		Groups []GroupDetails `json:"groups"`
	}
	err := c.Call(ctx, MethodGroupList, map[string]any{}, &result)
	return result.Groups, err
}

type RemoteState struct {
	State       string `json:"state"`
	Destination string `json:"destination,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

func (c *Client) RemoteState(ctx context.Context, workspaceID string) (*RemoteState, error) {
	var result struct {
		Remote struct {
			Enabled     bool    `json:"enabled"`
			State       string  `json:"state"`
			Destination *string `json:"destination"`
			Detail      *string `json:"detail"`
		} `json:"remote"`
	}
	if err := c.Call(ctx, MethodRemoteStatus, map[string]any{"workspace_id": workspaceID}, &result); err != nil || !result.Remote.Enabled {
		return nil, err
	}
	state := &RemoteState{State: result.Remote.State}
	if result.Remote.Destination != nil {
		state.Destination = *result.Remote.Destination
	}
	if result.Remote.Detail != nil {
		state.Detail = *result.Remote.Detail
	}
	return state, nil
}

func (c *Client) CloseTerminal(ctx context.Context, workspaceID, surfaceID string, force bool) error {
	return c.Call(ctx, MethodCloseTerminal, map[string]any{"workspace_id": workspaceID, "surface_id": surfaceID, "force": force}, nil)
}

func (c *Client) RenameTerminal(ctx context.Context, workspaceID, surfaceID, title string) error {
	params := map[string]any{"workspace_id": workspaceID, "surface_id": surfaceID, "action": "rename", "title": title}
	if title == "" {
		params = map[string]any{"workspace_id": workspaceID, "surface_id": surfaceID, "action": "clear_name"}
	}
	return c.Call(ctx, MethodRenameTerminal, params, nil)
}

func (c *Client) RestartTerminal(ctx context.Context, workspaceID, surfaceID, command string) error {
	return c.Call(ctx, MethodRestartTerminal, map[string]any{"workspace_id": workspaceID, "surface_id": surfaceID, "command": command}, nil)
}

type InputState struct {
	SurfaceID      string `json:"surface_id"`
	Agent          bool   `json:"agent"`
	Lifecycle      string `json:"lifecycle"`
	State          string `json:"state"`
	BlocksTyping   bool   `json:"blocks_typing"`
	WaitingOnHuman bool   `json:"waiting_on_human"`
}

func (c *Client) InputState(ctx context.Context, workspaceID, surfaceID string) (InputState, error) {
	var state InputState
	err := c.Call(ctx, MethodInputState, map[string]any{"workspace_id": workspaceID, "surface_id": surfaceID}, &state)
	return state, err
}

func (c *Client) Notifications(ctx context.Context) ([]Notification, error) {
	var list struct {
		Notifications []Notification `json:"notifications"`
	}
	err := c.Call(ctx, MethodNotificationList, nil, &list)
	return list.Notifications, err
}

func (c *Client) MarkNotificationRead(ctx context.Context, id string) error {
	return c.Call(ctx, MethodMarkNotification, map[string]any{"id": id}, nil)
}

func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	return c.Call(ctx, MethodMarkNotification, map[string]any{"all": true}, nil)
}

func (c *Client) MarkSurfaceNotificationsRead(ctx context.Context, surfaceID string) error {
	list, err := c.Notifications(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, notification := range list {
		if notification.IsRead || notification.SurfaceID != surfaceID {
			continue
		}
		if err := c.MarkNotificationRead(ctx, notification.ID); err != nil && !IsNotFound(err) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

type FeedOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type FeedQuestion struct {
	ID          string       `json:"id"`
	Header      string       `json:"header"`
	Prompt      string       `json:"prompt"`
	MultiSelect bool         `json:"multi_select"`
	Options     []FeedOption `json:"options"`
}

type FeedItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status"`
	RequestID string         `json:"request_id"`
	Source    string         `json:"source"`
	Title     string         `json:"title"`
	ToolName  string         `json:"tool_name"`
	ToolInput string         `json:"tool_input"`
	Questions []FeedQuestion `json:"questions"`
	Plan      string         `json:"plan"`
	Cwd       string         `json:"cwd"`
	CreatedAt string         `json:"created_at"`
}

func (c *Client) FeedItems(ctx context.Context) ([]FeedItem, error) {
	var list struct {
		Items []FeedItem `json:"items"`
	}
	err := c.Call(ctx, MethodFeedList, nil, &list)
	return list.Items, err
}

func (c *Client) ReplyPermission(ctx context.Context, requestID, mode string) error {
	return c.Call(ctx, "feed.permission.reply", map[string]any{"request_id": requestID, "mode": mode}, nil)
}

func (c *Client) ReplyExitPlan(ctx context.Context, requestID, mode, feedback string) error {
	params := map[string]any{"request_id": requestID, "mode": mode}
	if feedback != "" {
		params["feedback"] = feedback
	}
	return c.Call(ctx, "feed.exit_plan.reply", params, nil)
}

func (c *Client) ReplyQuestion(ctx context.Context, requestID string, selections []string) error {
	return c.Call(ctx, "feed.question.reply", map[string]any{"request_id": requestID, "selections": selections}, nil)
}

type Event struct {
	Type        string          `json:"type"`
	Seq         int64           `json:"seq"`
	Name        string          `json:"name"`
	WorkspaceID *string         `json:"workspace_id"`
	SurfaceID   *string         `json:"surface_id"`
	Payload     json.RawMessage `json:"payload"`
}

const hookEventPrefix = "agent.hook."

type eventTarget struct {
	WorkspaceID   string `json:"workspace_id"`
	SurfaceID     string `json:"surface_id"`
	HookEventName string `json:"hook_event_name"`
}

func (e Event) target() eventTarget {
	var target eventTarget
	_ = json.Unmarshal(e.Payload, &target)
	return target
}

func (e Event) Workspace() string {
	if e.WorkspaceID != nil && *e.WorkspaceID != "" {
		return *e.WorkspaceID
	}
	return e.target().WorkspaceID
}

func (e Event) Surface() string {
	if e.SurfaceID != nil && *e.SurfaceID != "" {
		return *e.SurfaceID
	}
	return e.target().SurfaceID
}

func (e Event) Hook() (string, bool) {
	name, ok := strings.CutPrefix(e.Name, hookEventPrefix)
	if !ok {
		return "", false
	}
	return cmp.Or(e.target().HookEventName, name), true
}

type EventFilter struct {
	Categories []string
	Names      []string
}

func (c *Client) Events(ctx context.Context, afterSeq *int64, filter EventFilter, handle func(Event) error) error {
	params := map[string]any{"include_heartbeats": true}
	if len(filter.Categories) > 0 {
		params["categories"] = filter.Categories
	}
	if len(filter.Names) > 0 {
		params["names"] = filter.Names
	}
	if afterSeq != nil {
		params["after_seq"] = *afterSeq
	}
	return c.Stream(ctx, params, func(line json.RawMessage) error {
		if bytes.HasPrefix(line, []byte("ERROR:")) {
			_, err := decodeResponse(line)
			return err
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		if event.Type == "" {
			_, err := decodeResponse(line)
			return err
		}
		return handle(event)
	})
}

type AgentMessage struct {
	ID                 string   `json:"id"`
	ThreadID           string   `json:"thread_id"`
	SenderName         string   `json:"sender_name"`
	RecipientSurfaceID string   `json:"recipient_surface_id"`
	Body               string   `json:"body"`
	State              string   `json:"state"`
	CreatedAt          float64  `json:"created_at"`
	DeliveredAt        *float64 `json:"delivered_at"`
	ReadAt             *float64 `json:"read_at"`
	FailureReason      string   `json:"failure_reason"`
}

func (c *Client) SendAgentMessage(ctx context.Context, target, from, body string) (AgentMessage, error) {
	var message AgentMessage
	err := c.Call(ctx, MethodAgentMessageSend, map[string]any{"target": target, "from": from, "body": body}, &message)
	return message, err
}

func (c *Client) AgentMessages(ctx context.Context, surfaceID string, limit int) ([]AgentMessage, error) {
	var list struct {
		Messages []AgentMessage `json:"messages"`
	}
	err := c.Call(ctx, MethodAgentMessageList, map[string]any{"surface": surfaceID, "limit": limit}, &list)
	return list.Messages, err
}

type keepAwakeStatus struct {
	Enabled bool `json:"enabled"`
}

func (c *Client) KeepAwake(ctx context.Context) (bool, error) {
	var status keepAwakeStatus
	err := c.Call(ctx, MethodKeepAwake, nil, &status)
	return status.Enabled, err
}

func (c *Client) SetKeepAwake(ctx context.Context, enabled bool) (bool, error) {
	var status keepAwakeStatus
	err := c.Call(ctx, MethodSetKeepAwake, map[string]any{"enabled": enabled}, &status)
	return status.Enabled, err
}

type Resources struct {
	CPUPercent   float64 `json:"cpu_percent"`
	MemoryBytes  int64   `json:"memory_bytes"`
	ProcessCount int     `json:"process_count"`
}

type TopProcess struct {
	PID       int          `json:"pid"`
	Name      string       `json:"name"`
	Path      string       `json:"path"`
	Resources Resources    `json:"resources"`
	Children  []TopProcess `json:"children"`
}

type TopWorkspace struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Resources Resources `json:"resources"`
	Panes     []struct {
		Surfaces []struct {
			ID        string       `json:"id"`
			Processes []TopProcess `json:"processes"`
		} `json:"surfaces"`
	} `json:"panes"`
}

type TopAgent struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Resources   Resources `json:"resources"`
}

type Top struct {
	Sample struct {
		SampledAt string `json:"sampled_at"`
	} `json:"sample"`
	Totals           Resources `json:"totals"`
	MemoryDiagnostic struct {
		App struct {
			PhysicalFootprintBytes int64 `json:"physical_footprint_bytes"`
		} `json:"app"`
	} `json:"memory_diagnostic"`
	Windows []struct {
		Workspaces []TopWorkspace `json:"workspaces"`
	} `json:"windows"`
	CodingAgents []TopAgent `json:"coding_agents"`
}

func (c *Client) Top(ctx context.Context) (Top, error) {
	var top Top
	err := c.Call(ctx, MethodTop, map[string]any{"include_processes": true}, &top)
	return top, err
}
