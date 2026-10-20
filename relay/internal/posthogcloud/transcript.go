package posthogcloud

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/claude"
)

const (
	StateWorking = "working"
	StateWaiting = "waiting"
	StateIdle    = "idle"
)

type logEntry struct {
	Timestamp    string `json:"timestamp"`
	EventID      string `json:"event_id"`
	FirstEventID string `json:"first_event_id"`
	Notification struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	} `json:"notification"`
}

type contentBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
	Content *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type toolMeta struct {
	ClaudeCode struct {
		ToolName string `json:"toolName"`
	} `json:"claudeCode"`
	PostHog struct {
		MCP struct {
			Server string `json:"server"`
			Tool   string `json:"tool"`
		} `json:"mcp"`
	} `json:"posthog"`
}

type planEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	RawOutput     json.RawMessage `json:"rawOutput"`
	Entries       []planEntry     `json:"entries"`
	Meta          toolMeta        `json:"_meta"`
}

type permissionParams struct {
	RequestID  string             `json:"requestId"`
	ToolCallID string             `json:"toolCallId"`
	OptionID   string             `json:"optionId"`
	Options    []PermissionOption `json:"options"`
	ToolCall   struct {
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Kind       string          `json:"kind"`
		RawInput   json.RawMessage `json:"rawInput"`
		Meta       struct {
			CodeToolKind string         `json:"codeToolKind"`
			Questions    []QuestionItem `json:"questions"`
			ClaudeCode   struct {
				ToolName string `json:"toolName"`
			} `json:"claudeCode"`
			PostHog struct {
				ToolName string `json:"toolName"`
			} `json:"posthog"`
		} `json:"_meta"`
	} `json:"toolCall"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type QuestionItem struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Options     []QuestionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

const (
	ApprovalPermission = "permission"
	ApprovalQuestion   = "question"
	ApprovalPlan       = "plan"
)

type PendingApproval struct {
	RequestID  string
	ToolCallID string
	Kind       string
	Tool       string
	Title      string
	Detail     string
	Plan       string
	Questions  []QuestionItem
	Options    []PermissionOption
	Time       string
}

type openChunk struct {
	id   string
	kind string
}

type approval struct {
	entryID string
	title   string
	options []PermissionOption
	pending PendingApproval
}

type Transcript struct {
	entries   []chat.Entry
	index     map[string]int
	fed       int
	turn      int
	open      *openChunk
	prompts   map[string]bool
	approvals map[string]*approval
	state     string
}

func NewTranscript() *Transcript {
	return &Transcript{index: map[string]int{}, prompts: map[string]bool{}, approvals: map[string]*approval{}}
}

func (t *Transcript) Entries() []chat.Entry {
	return append([]chat.Entry(nil), t.entries...)
}

func (t *Transcript) State() string {
	for _, pending := range t.approvals {
		if pending.options != nil {
			return StateWaiting
		}
	}
	return t.state
}

func (t *Transcript) Feed(raw []byte) []chat.Entry {
	t.fed++
	var entry logEntry
	if err := json.Unmarshal(raw, &entry); err != nil || entry.Notification.Method == "" {
		return nil
	}
	id := firstNonEmpty(entry.FirstEventID, entry.EventID)
	if id == "" {
		id = fmt.Sprintf("e%d", t.fed)
	}
	params := entry.Notification.Params
	switch entry.Notification.Method {
	case "session/prompt":
		t.open = nil
		t.turn++
		t.state = StateWorking
		return t.prompt(id, entry.Timestamp, params)
	case "session/update":
		var envelope struct {
			Update sessionUpdate `json:"update"`
		}
		if json.Unmarshal(params, &envelope) != nil {
			return nil
		}
		return t.update(id, entry.Timestamp, envelope.Update)
	case "session/request_permission", "_posthog/permission_request":
		t.open = nil
		var request permissionParams
		if json.Unmarshal(params, &request) != nil {
			return nil
		}
		return t.requestPermission(entry.Timestamp, request)
	case "_posthog/permission_resolved":
		var resolved permissionParams
		if json.Unmarshal(params, &resolved) != nil {
			return nil
		}
		return t.resolvePermission(resolved)
	case "_posthog/turn_complete", "_posthog/background_turn_complete":
		t.open = nil
		t.state = StateIdle
		var done struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(params, &done)
		changed := t.settleApprovals()
		if text := stopText(done.StopReason); text != "" {
			changed = append(changed, t.add(chat.Entry{ID: id, Kind: chat.KindNotice, Text: text, Time: entry.Timestamp})...)
		}
		return changed
	case "_posthog/background_turn_started":
		t.state = StateWorking
	case "_posthog/error", "_posthog/initialization_failed":
		t.open = nil
		var failure struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(params, &failure)
		text := strings.TrimSpace(firstNonEmpty(failure.Message, failure.Error))
		if text == "" {
			text = "The agent hit an error"
		}
		return t.add(chat.Entry{ID: id, Kind: chat.KindNotice, Text: chat.Truncate(text, chat.MaxSummaryLength), Time: entry.Timestamp})
	case "_posthog/compact_boundary":
		t.open = nil
		return t.add(chat.Entry{ID: id, Kind: chat.KindNotice, Text: "Compacted the conversation", Time: entry.Timestamp})
	case "_posthog/conversation_cleared":
		t.open = nil
		return t.add(chat.Entry{ID: id, Kind: chat.KindNotice, Text: "Cleared the conversation", Time: entry.Timestamp})
	}
	return nil
}

func (t *Transcript) FeedPermissionRequest(raw []byte, timestamp string) []chat.Entry {
	var request permissionParams
	if json.Unmarshal(raw, &request) != nil {
		return nil
	}
	t.open = nil
	return t.requestPermission(timestamp, request)
}

func (t *Transcript) prompt(id, timestamp string, params json.RawMessage) []chat.Entry {
	var request struct {
		Prompt []contentBlock `json:"prompt"`
	}
	if json.Unmarshal(params, &request) != nil {
		return nil
	}
	text := blocksText(request.Prompt)
	if text == "" {
		return nil
	}
	t.prompts[text] = true
	return t.add(chat.Entry{ID: id, Kind: chat.KindUser, Text: text, Time: timestamp})
}

func (t *Transcript) update(id, timestamp string, update sessionUpdate) []chat.Entry {
	switch update.SessionUpdate {
	case "user_message_chunk":
		text := contentText(update.Content)
		if t.open == nil && t.prompts[strings.TrimSpace(text)] {
			delete(t.prompts, strings.TrimSpace(text))
			return nil
		}
		return t.chunk(id, timestamp, chat.KindUser, text)
	case "agent_message_chunk":
		return t.chunk(id, timestamp, chat.KindAssistant, contentText(update.Content))
	case "agent_thought_chunk":
		return t.chunk(id, timestamp, chat.KindThinking, contentText(update.Content))
	case "agent_message":
		t.open = nil
		return t.text(id, timestamp, chat.KindAssistant, contentText(update.Content))
	case "agent_thought":
		t.open = nil
		return t.text(id, timestamp, chat.KindThinking, contentText(update.Content))
	case "tool_call", "tool_call_update":
		t.open = nil
		if update.ToolCallID == "" {
			return nil
		}
		return t.tool(timestamp, update)
	case "plan":
		t.open = nil
		todos := make([]chat.Todo, 0, len(update.Entries))
		for _, item := range update.Entries {
			todos = append(todos, chat.Todo{Content: item.Content, Status: item.Status})
		}
		return t.add(chat.Entry{ID: fmt.Sprintf("plan:%d", t.turn), Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{
			Name:   "Plan",
			Status: chat.ToolDone,
			Todos:  todos,
		}})
	}
	return nil
}

func (t *Transcript) chunk(id, timestamp, kind, text string) []chat.Entry {
	if t.open != nil && t.open.kind == kind {
		position := t.index[t.open.id]
		updated := t.entries[position]
		updated.Text = capText(kind, updated.Text+text)
		return t.add(updated)
	}
	t.open = &openChunk{id: id, kind: kind}
	if strings.TrimSpace(text) == "" {
		return t.add(chat.Entry{ID: id, Kind: kind, Text: text, Time: timestamp})
	}
	return t.add(chat.Entry{ID: id, Kind: kind, Text: capText(kind, text), Time: timestamp})
}

func (t *Transcript) text(id, timestamp, kind, text string) []chat.Entry {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return t.add(chat.Entry{ID: id, Kind: kind, Text: capText(kind, text), Time: timestamp})
}

func (t *Transcript) tool(timestamp string, update sessionUpdate) []chat.Entry {
	entry := chat.Entry{ID: update.ToolCallID, Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{Status: chat.ToolRunning}}
	if position, ok := t.index[update.ToolCallID]; ok {
		entry = t.entries[position]
		copied := *entry.Tool
		entry.Tool = &copied
	}
	tool := entry.Tool
	if name := toolName(update); name != "" {
		tool.Name = name
	}
	if tool.Name == "" {
		tool.Name = "Tool"
	}
	if summary := toolSummary(tool.Name, update); summary != "" {
		tool.Summary = summary
	}
	switch update.Status {
	case "completed":
		tool.Status = chat.ToolDone
	case "failed":
		tool.Status = chat.ToolFailed
	}
	var blocks []contentBlock
	_ = json.Unmarshal(update.Content, &blocks)
	if output := toolOutput(blocks, update.RawOutput); output != "" && update.SessionUpdate == "tool_call_update" {
		tool.Output = chat.Truncate(output, chat.MaxOutputLength)
	}
	if diff := diffHunks(blocks); diff != nil {
		tool.Diff = diff
	}
	return t.add(entry)
}

func (t *Transcript) requestPermission(timestamp string, request permissionParams) []chat.Entry {
	key := firstNonEmpty(request.ToolCallID, request.ToolCall.ToolCallID, request.RequestID)
	if key == "" {
		return nil
	}
	pending, known := t.approvals[key]
	if !known {
		pending = &approval{entryID: "approval:" + key}
		t.approvals[key] = pending
	}
	if request.ToolCall.Title != "" {
		pending.title = request.ToolCall.Title
	}
	if pending.options == nil && (!known || pending.pending.RequestID == "") {
		pending.options = request.Options
	}
	if request.RequestID != "" && pending.pending.RequestID == "" {
		pending.pending = describeApproval(key, timestamp, request)
	}
	if known {
		return nil
	}
	return t.add(chat.Entry{ID: pending.entryID, Kind: chat.KindNotice, Text: "Waiting for approval: " + titleOr(pending.title), Time: timestamp})
}

func (t *Transcript) resolvePermission(resolved permissionParams) []chat.Entry {
	key := firstNonEmpty(resolved.ToolCallID, resolved.RequestID)
	pending, ok := t.approvals[key]
	if !ok {
		return nil
	}
	verdict := "Answered"
	for _, option := range pending.options {
		if option.OptionID != resolved.OptionID {
			continue
		}
		switch {
		case strings.HasPrefix(option.Kind, "allow"):
			verdict = "Allowed"
		case strings.HasPrefix(option.Kind, "reject"):
			verdict = "Declined"
		}
	}
	return t.settle(pending, verdict)
}

func (t *Transcript) Pending() []PendingApproval {
	var pending []PendingApproval
	for _, candidate := range t.approvals {
		if candidate.options != nil && candidate.pending.RequestID != "" {
			pending = append(pending, candidate.pending)
		}
	}
	slices.SortFunc(pending, func(a, b PendingApproval) int { return strings.Compare(a.Time+a.RequestID, b.Time+b.RequestID) })
	return pending
}

func (t *Transcript) Resolve(requestID, optionID string) []chat.Entry {
	for key, candidate := range t.approvals {
		if candidate.pending.RequestID == requestID && candidate.options != nil {
			return t.resolvePermission(permissionParams{ToolCallID: key, OptionID: optionID})
		}
	}
	return nil
}

func describeApproval(key, timestamp string, request permissionParams) PendingApproval {
	call := request.ToolCall
	pending := PendingApproval{
		RequestID:  request.RequestID,
		ToolCallID: key,
		Kind:       ApprovalPermission,
		Tool:       firstNonEmpty(call.Meta.ClaudeCode.ToolName, call.Meta.PostHog.ToolName, toolKinds[call.Kind]),
		Title:      call.Title,
		Options:    request.Options,
		Time:       timestamp,
	}
	var input map[string]any
	_ = json.Unmarshal(call.RawInput, &input)
	plan, _ := input["plan"].(string)
	switch {
	case call.Meta.CodeToolKind == "question" && len(call.Meta.Questions) > 0:
		pending.Kind = ApprovalQuestion
		pending.Questions = call.Meta.Questions
	case plan != "" || hasOption(request.Options, "reject_with_feedback"):
		pending.Kind = ApprovalPlan
		pending.Plan = plan
	default:
		if _, value := claude.ToolInputValue(input); value != "" {
			pending.Detail = chat.Truncate(strings.TrimSpace(value), 8000)
		} else if call.Kind == "execute" {
			pending.Detail = shellCommand(call.Title)
		}
	}
	return pending
}

func hasOption(options []PermissionOption, optionID string) bool {
	return slices.ContainsFunc(options, func(option PermissionOption) bool { return option.OptionID == optionID })
}

func (t *Transcript) settleApprovals() []chat.Entry {
	var changed []chat.Entry
	for _, pending := range t.approvals {
		if pending.options != nil {
			changed = append(changed, t.settle(pending, "Asked for approval")...)
		}
	}
	return changed
}

func (t *Transcript) settle(pending *approval, verdict string) []chat.Entry {
	pending.options = nil
	position, ok := t.index[pending.entryID]
	if !ok {
		return nil
	}
	updated := t.entries[position]
	updated.Text = verdict + ": " + titleOr(pending.title)
	return t.add(updated)
}

func (t *Transcript) add(entry chat.Entry) []chat.Entry {
	if position, ok := t.index[entry.ID]; ok {
		t.entries[position] = entry
		return []chat.Entry{entry}
	}
	t.index[entry.ID] = len(t.entries)
	t.entries = append(t.entries, entry)
	return []chat.Entry{entry}
}

func toolName(update sessionUpdate) string {
	if name := update.Meta.ClaudeCode.ToolName; name != "" {
		return name
	}
	if mcp := update.Meta.PostHog.MCP; mcp.Tool != "" {
		return mcp.Server + "/" + mcp.Tool
	}
	if name, ok := toolKinds[update.Kind]; ok {
		return name
	}
	return firstLine(update.Title)
}

var toolKinds = map[string]string{
	"execute": "Bash",
	"read":    "Read",
	"edit":    "Edit",
	"delete":  "Delete",
	"move":    "Move",
	"search":  "Search",
	"fetch":   "Fetch",
	"think":   "Think",
}

var shellWrapper = regexp.MustCompile(`^(?:/bin/)?(?:ba|z)?sh -l?c '(.*)'$`)

func shellCommand(title string) string {
	if match := shellWrapper.FindStringSubmatch(strings.TrimSpace(title)); match != nil {
		return match[1]
	}
	return title
}

func toolSummary(name string, update sessionUpdate) string {
	var input map[string]any
	if json.Unmarshal(update.RawInput, &input) == nil {
		if field, value := claude.ToolInputValue(input); value != "" {
			if field == "file_path" || field == "path" {
				return chat.ShortPath(value)
			}
			return firstLine(value)
		}
	}
	title := update.Title
	if update.Kind == "execute" {
		title = shellCommand(title)
	}
	if title := firstLine(title); title != name {
		return title
	}
	return ""
}

func toolOutput(blocks []contentBlock, rawOutput json.RawMessage) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "content" && b.Content != nil && b.Content.Type == "text" && strings.TrimSpace(b.Content.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Content.Text))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	var text string
	if json.Unmarshal(rawOutput, &text) == nil {
		return strings.TrimSpace(text)
	}
	var wrapped struct {
		Content []contentBlock `json:"content"`
	}
	if json.Unmarshal(rawOutput, &wrapped) == nil {
		return blocksText(wrapped.Content)
	}
	return ""
}

func diffHunks(blocks []contentBlock) []chat.Hunk {
	var hunks []chat.Hunk
	for _, b := range blocks {
		if b.Type != "diff" {
			continue
		}
		var lines []string
		if b.OldText != "" {
			for _, text := range strings.Split(strings.TrimSuffix(b.OldText, "\n"), "\n") {
				lines = append(lines, "-"+text)
			}
		}
		for _, text := range strings.Split(strings.TrimSuffix(b.NewText, "\n"), "\n") {
			lines = append(lines, "+"+text)
		}
		oldStart := 1
		if b.OldText == "" {
			oldStart = 0
		}
		hunks = append(hunks, chat.Hunk{OldStart: oldStart, NewStart: 1, Lines: lines})
	}
	if hunks == nil {
		return nil
	}
	return chat.CapHunks(hunks)
}

func contentText(raw json.RawMessage) string {
	var b contentBlock
	if json.Unmarshal(raw, &b) == nil && b.Type == "text" {
		return b.Text
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocksText(blocks)
	}
	return ""
}

func blocksText(blocks []contentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func capText(kind, text string) string {
	if kind == chat.KindThinking {
		return chat.Truncate(text, chat.MaxThinkingLength)
	}
	return text
}

func stopText(reason string) string {
	switch reason {
	case "cancelled":
		return "Interrupted"
	case "max_tokens", "max_turn_requests":
		return "Stopped at the agent's limit"
	case "refusal":
		return "The agent declined to continue"
	}
	return ""
}

func titleOr(title string) string {
	if title == "" {
		return "a tool call"
	}
	return title
}

func firstLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return chat.Truncate(first, chat.MaxSummaryLength)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
