package pi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/claude"
)

var toolNames = map[string]string{
	"bash":  "Bash",
	"read":  "Read",
	"edit":  "Edit",
	"write": "Write",
	"grep":  "Grep",
	"find":  "Find",
	"ls":    "List",
}

type line struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  string          `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type message struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	IsError      bool            `json:"isError"`
	Details      json.RawMessage `json:"details"`
	Command      string          `json:"command"`
	Output       string          `json:"output"`
	ExitCode     *int            `json:"exitCode"`
	Cancelled    bool            `json:"cancelled"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Transcript struct {
	entries []chat.Entry
	index   map[string]int
	lastID  string
}

func NewTranscript() *Transcript {
	return &Transcript{index: map[string]int{}}
}

func (t *Transcript) Entries() []chat.Entry {
	return append([]chat.Entry(nil), t.entries...)
}

func (t *Transcript) Feed(raw []byte) []chat.Entry {
	var entry line
	if err := json.Unmarshal(raw, &entry); err != nil || entry.ID == "" || entry.Type == "session" {
		return nil
	}
	var changed []chat.Entry
	if t.lastID != "" && entry.ParentID != t.lastID {
		changed = t.add(chat.Entry{ID: "branch:" + entry.ID, Kind: chat.KindNotice, Text: "Went back to an earlier point in the conversation", Time: entry.Timestamp})
	}
	t.lastID = entry.ID
	switch entry.Type {
	case "compaction":
		return append(changed, t.add(chat.Entry{ID: entry.ID, Kind: chat.KindNotice, Text: "Compacted the conversation", Time: entry.Timestamp})...)
	case "message":
		var msg message
		if json.Unmarshal(entry.Message, &msg) != nil {
			return changed
		}
		return append(changed, t.message(entry, msg)...)
	}
	return changed
}

func (t *Transcript) message(entry line, msg message) []chat.Entry {
	switch msg.Role {
	case "user":
		return t.user(entry, msg.Content)
	case "assistant":
		return t.assistant(entry, msg)
	case "toolResult":
		return t.toolResult(msg)
	case "bashExecution":
		status := chat.ToolDone
		if msg.Cancelled || (msg.ExitCode != nil && *msg.ExitCode != 0) {
			status = chat.ToolFailed
		}
		return t.add(chat.Entry{ID: entry.ID, Kind: chat.KindTool, Time: entry.Timestamp, Tool: &chat.Tool{
			Name:    toolNames["bash"],
			Summary: firstLine(msg.Command),
			Status:  status,
			Output:  chat.Truncate(strings.TrimSpace(msg.Output), chat.MaxOutputLength),
		}})
	}
	return nil
}

func (t *Transcript) user(entry line, content json.RawMessage) []chat.Entry {
	var text string
	var images []string
	if json.Unmarshal(content, &text) != nil {
		var blocks []block
		if json.Unmarshal(content, &blocks) != nil {
			return nil
		}
		var parts []string
		for i, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, b.Text)
			case "image":
				images = append(images, fmt.Sprintf("%s:%d", entry.ID, i))
			}
		}
		text = strings.Join(parts, "\n")
	}
	attached, text := chat.SplitAttachedImages(strings.TrimSpace(text))
	images = append(attached, images...)
	text = strings.TrimSpace(text)
	if text == "" && len(images) == 0 {
		return nil
	}
	return t.add(chat.Entry{ID: entry.ID, Kind: chat.KindUser, Text: text, Images: images, Time: entry.Timestamp})
}

func (t *Transcript) assistant(entry line, msg message) []chat.Entry {
	var blocks []block
	_ = json.Unmarshal(msg.Content, &blocks)
	var changed []chat.Entry
	for i, b := range blocks {
		id := fmt.Sprintf("%s:%d", entry.ID, i)
		switch b.Type {
		case "text":
			if text := strings.TrimSpace(b.Text); text != "" {
				changed = append(changed, t.add(chat.Entry{ID: id, Kind: chat.KindAssistant, Text: text, Time: entry.Timestamp})...)
			}
		case "thinking":
			if text := strings.TrimSpace(b.Thinking); text != "" {
				changed = append(changed, t.add(chat.Entry{ID: id, Kind: chat.KindThinking, Text: chat.Truncate(text, chat.MaxThinkingLength), Time: entry.Timestamp})...)
			}
		case "toolCall":
			if b.ID != "" {
				changed = append(changed, t.add(chat.Entry{ID: b.ID, Kind: chat.KindTool, Time: entry.Timestamp, Tool: toolCall(b)})...)
			}
		}
	}
	switch msg.StopReason {
	case "aborted":
		changed = append(changed, t.add(chat.Entry{ID: entry.ID + ":stop", Kind: chat.KindNotice, Text: "Interrupted", Time: entry.Timestamp})...)
	case "error":
		text := strings.TrimSpace(msg.ErrorMessage)
		if text == "" {
			text = "The request failed"
		}
		changed = append(changed, t.add(chat.Entry{ID: entry.ID + ":stop", Kind: chat.KindNotice, Text: chat.Truncate(text, chat.MaxSummaryLength), Time: entry.Timestamp})...)
	}
	return changed
}

func (t *Transcript) toolResult(msg message) []chat.Entry {
	position, ok := t.index[msg.ToolCallID]
	if !ok || msg.ToolCallID == "" {
		return nil
	}
	updated := t.entries[position]
	tool := *updated.Tool
	tool.Status = chat.ToolDone
	if msg.IsError {
		tool.Status = chat.ToolFailed
	}
	tool.Output = chat.Truncate(resultText(msg.Content), chat.MaxOutputLength)
	if msg.ToolName == "edit" && !msg.IsError {
		var details struct {
			Patch string `json:"patch"`
		}
		if json.Unmarshal(msg.Details, &details) == nil {
			tool.Diff = chat.UnifiedDiffHunks(details.Patch)
		}
	}
	updated.Tool = &tool
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

func toolCall(b block) *chat.Tool {
	var input map[string]any
	_ = json.Unmarshal(b.Arguments, &input)
	tool := &chat.Tool{Name: b.Name, Status: chat.ToolRunning}
	if name, ok := toolNames[b.Name]; ok {
		tool.Name = name
	}
	text := func(field string) string {
		value, _ := input[field].(string)
		return value
	}
	switch b.Name {
	case "bash":
		tool.Summary = firstLine(text("command"))
	case "read", "edit", "ls":
		tool.Summary = chat.ShortPath(text("path"))
	case "write":
		tool.Summary = chat.ShortPath(text("path"))
		tool.Diff = addedFile(text("content"))
	case "grep", "find":
		tool.Summary = firstLine(text("pattern"))
	default:
		_, value := claude.ToolInputValue(input)
		tool.Summary = firstLine(value)
	}
	return tool
}

func addedFile(content string) []chat.Hunk {
	if content == "" {
		return nil
	}
	var lines []string
	for _, text := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		lines = append(lines, "+"+text)
	}
	return chat.CapHunks([]chat.Hunk{{OldStart: 0, NewStart: 1, Lines: lines}})
}

func resultText(raw json.RawMessage) string {
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func firstLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return chat.Truncate(first, chat.MaxSummaryLength)
}
