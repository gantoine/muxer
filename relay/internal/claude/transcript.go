package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/gantoine/muxer/relay/internal/chat"
)

type line struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
	ToolResult  json.RawMessage `json:"toolUseResult"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type Transcript struct {
	entries    []chat.Entry
	index      map[string]int
	sentImages map[string]bool
}

func NewTranscript() *Transcript {
	return &Transcript{index: map[string]int{}, sentImages: map[string]bool{}}
}

func (t *Transcript) Entries() []chat.Entry {
	return append([]chat.Entry(nil), t.entries...)
}

func (t *Transcript) Feed(raw []byte) []chat.Entry {
	var entry line
	if err := json.Unmarshal(raw, &entry); err != nil || entry.IsSidechain || entry.IsMeta || len(entry.Message) == 0 {
		return nil
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		return nil
	}
	var msg message
	if err := json.Unmarshal(entry.Message, &msg); err != nil {
		return nil
	}
	var text string
	if err := json.Unmarshal(msg.Content, &text); err == nil {
		return t.addText(entry, 0, text)
	}
	var blocks []block
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil
	}
	var changed []chat.Entry
	var images []string
	results := 0
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results++
		}
	}
	for i, b := range blocks {
		switch b.Type {
		case "text":
			changed = append(changed, t.addText(entry, i, b.Text)...)
		case "thinking":
			if entry.Type == "assistant" && strings.TrimSpace(b.Thinking) != "" {
				changed = append(changed, t.upsert(chat.Entry{
					ID:   fmt.Sprintf("%s:%d", entry.UUID, i),
					Kind: chat.KindThinking,
					Text: chat.Truncate(strings.TrimSpace(b.Thinking), chat.MaxThinkingLength),
					Time: entry.Timestamp,
				}))
			}
		case "image":
			if entry.Type == "user" {
				images = append(images, fmt.Sprintf("%s:%d", entry.UUID, i))
			}
		case "tool_use":
			if readsSentImage(b.Name, b.Input) {
				t.sentImages[b.ID] = true
			}
			changed = append(changed, t.upsert(chat.Entry{
				ID:   b.ID,
				Kind: chat.KindTool,
				Time: entry.Timestamp,
				Tool: &chat.Tool{Name: b.Name, Summary: summarize(b.Name, b.Input), Status: chat.ToolRunning, Todos: todos(b.Name, b.Input)},
			}))
		case "tool_result":
			position, ok := t.index[b.ToolUseID]
			if !ok {
				continue
			}
			updated := t.entries[position]
			tool := *updated.Tool
			tool.Status = chat.ToolDone
			if b.IsError {
				tool.Status = chat.ToolFailed
			}
			tool.Output = chat.Truncate(resultText(b.Content), chat.MaxOutputLength)
			if results == 1 && !b.IsError {
				tool.Diff = diffHunks(entry.ToolResult)
			}
			if !t.sentImages[b.ToolUseID] {
				tool.Images = resultImages(b.Content, fmt.Sprintf("%s:%d", entry.UUID, i))
			}
			updated.Tool = &tool
			changed = append(changed, t.upsert(updated))
		}
	}
	if len(images) > 0 {
		changed = t.attachImages(entry, changed, images)
	}
	return changed
}

func (t *Transcript) attachImages(entry line, changed []chat.Entry, images []string) []chat.Entry {
	prefix := entry.UUID + ":"
	for i, e := range changed {
		if e.Kind == chat.KindUser && strings.HasPrefix(e.ID, prefix) {
			e.Images = append(append([]string(nil), e.Images...), images...)
			changed[i] = t.upsert(e)
			return changed
		}
	}
	return append(changed, t.upsert(chat.Entry{ID: prefix + "images", Kind: chat.KindUser, Images: images, Time: entry.Timestamp}))
}

func todos(name string, raw json.RawMessage) []chat.Todo {
	if name != "TodoWrite" {
		return nil
	}
	var input struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
		} `json:"todos"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	var items []chat.Todo
	for _, todo := range input.Todos {
		if strings.TrimSpace(todo.Content) == "" {
			continue
		}
		items = append(items, chat.Todo{
			Content:    chat.Truncate(strings.TrimSpace(todo.Content), chat.MaxSummaryLength),
			Status:     todo.Status,
			ActiveForm: chat.Truncate(strings.TrimSpace(todo.ActiveForm), chat.MaxSummaryLength),
		})
	}
	return items
}

func diffHunks(raw json.RawMessage) []chat.Hunk {
	var result struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Patch   []struct {
			OldStart int      `json:"oldStart"`
			NewStart int      `json:"newStart"`
			Lines    []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &result) != nil {
		return nil
	}
	var hunks []chat.Hunk
	if len(result.Patch) == 0 && result.Type == "create" && result.Content != "" {
		var lines []string
		for _, text := range strings.Split(strings.TrimSuffix(result.Content, "\n"), "\n") {
			lines = append(lines, "+"+text)
		}
		hunks = []chat.Hunk{{OldStart: 0, NewStart: 1, Lines: lines}}
	}
	for _, patch := range result.Patch {
		hunks = append(hunks, chat.Hunk{OldStart: patch.OldStart, NewStart: patch.NewStart, Lines: patch.Lines})
	}
	return chat.CapHunks(hunks)
}

func readsSentImage(name string, raw json.RawMessage) bool {
	var input struct {
		FilePath string `json:"file_path"`
	}
	return name == "Read" && json.Unmarshal(raw, &input) == nil && chat.IsAttachedImage(input.FilePath)
}

func resultImages(raw json.RawMessage, prefix string) []string {
	var items []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	var images []string
	for i, item := range items {
		if item.Type == "image" {
			images = append(images, fmt.Sprintf("%s:%d", prefix, i))
		}
	}
	return images
}

func (t *Transcript) addText(entry line, position int, text string) []chat.Entry {
	kind := chat.KindAssistant
	if entry.Type == "user" {
		kind = chat.KindUser
	}
	text, kind = cleanText(text, kind)
	var images []string
	if kind == chat.KindUser {
		images, text = splitAttachedImages(text)
	}
	if text == "" && len(images) == 0 {
		return nil
	}
	return []chat.Entry{t.upsert(chat.Entry{ID: fmt.Sprintf("%s:%d", entry.UUID, position), Kind: kind, Text: text, Images: images, Time: entry.Timestamp})}
}

func (t *Transcript) upsert(entry chat.Entry) chat.Entry {
	if position, ok := t.index[entry.ID]; ok {
		t.entries[position] = entry
		return entry
	}
	t.index[entry.ID] = len(t.entries)
	t.entries = append(t.entries, entry)
	return entry
}

var (
	commandName    = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgs    = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	localOutput    = regexp.MustCompile(`(?s)^\s*<local-command-(stdout|stderr)>(.*?)</local-command-(stdout|stderr)>\s*$`)
	systemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	imageMarker    = regexp.MustCompile(`\[Image(?: #\d+|: source: [^\]]*)\]`)
)

func splitAttachedImages(text string) ([]string, string) {
	var images []string
	for {
		match := chat.AttachedImage.FindStringSubmatch(text)
		if match == nil {
			return images, text
		}
		images = append(images, match[1])
		text = text[len(match[0]):]
	}
}

func cleanText(text, kind string) (string, string) {
	text = strings.TrimSpace(systemReminder.ReplaceAllString(text, ""))
	if kind != chat.KindUser {
		return text, kind
	}
	if match := commandName.FindStringSubmatch(text); match != nil {
		command := strings.TrimSpace(match[1])
		if args := commandArgs.FindStringSubmatch(text); args != nil && strings.TrimSpace(args[1]) != "" {
			command += " " + strings.TrimSpace(args[1])
		}
		return command, chat.KindUser
	}
	if match := localOutput.FindStringSubmatch(text); match != nil {
		return strings.TrimSpace(match[2]), chat.KindNotice
	}
	if strings.HasPrefix(text, "[Request interrupted") {
		return "Interrupted", chat.KindNotice
	}
	return strings.TrimSpace(imageMarker.ReplaceAllString(text, "")), kind
}

func resultText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var blocks []block
	if err := json.Unmarshal(raw, &blocks); err != nil {
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

var toolInputFields = []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt", "skill"}

func ToolInputValue(input map[string]any) (field, value string) {
	for _, field := range toolInputFields {
		if value, ok := input[field].(string); ok && strings.TrimSpace(value) != "" {
			return field, value
		}
	}
	return "", ""
}

func summarize(name string, raw json.RawMessage) string {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	field, value := ToolInputValue(input)
	if field == "file_path" && name != "Bash" {
		value = chat.ShortPath(value)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return chat.Truncate(first, chat.MaxSummaryLength)
}
