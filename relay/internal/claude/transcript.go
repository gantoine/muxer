package claude

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	maxSummaryLength = 200
	maxOutputLength  = 2000
)

type Entry struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Text   string   `json:"text,omitempty"`
	Images []string `json:"images,omitempty"`
	Tool   *Tool    `json:"tool,omitempty"`
	Time   string   `json:"time,omitempty"`
}

type Tool struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
}

const (
	KindUser      = "user"
	KindAssistant = "assistant"
	KindTool      = "tool"
	KindNotice    = "notice"

	ToolRunning = "running"
	ToolDone    = "done"
	ToolFailed  = "failed"
)

type line struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type Transcript struct {
	entries []Entry
	index   map[string]int
}

func NewTranscript() *Transcript {
	return &Transcript{index: map[string]int{}}
}

func (t *Transcript) Entries() []Entry {
	return append([]Entry(nil), t.entries...)
}

func (t *Transcript) Feed(raw []byte) []Entry {
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
	var changed []Entry
	for i, b := range blocks {
		switch b.Type {
		case "text":
			changed = append(changed, t.addText(entry, i, b.Text)...)
		case "tool_use":
			changed = append(changed, t.upsert(Entry{
				ID:   b.ID,
				Kind: KindTool,
				Time: entry.Timestamp,
				Tool: &Tool{Name: b.Name, Summary: summarize(b.Name, b.Input), Status: ToolRunning},
			}))
		case "tool_result":
			position, ok := t.index[b.ToolUseID]
			if !ok {
				continue
			}
			updated := t.entries[position]
			tool := *updated.Tool
			tool.Status = ToolDone
			if b.IsError {
				tool.Status = ToolFailed
			}
			tool.Output = Truncate(resultText(b.Content), maxOutputLength)
			updated.Tool = &tool
			changed = append(changed, t.upsert(updated))
		}
	}
	return changed
}

func (t *Transcript) addText(entry line, position int, text string) []Entry {
	kind := KindAssistant
	if entry.Type == "user" {
		kind = KindUser
	}
	text, kind = cleanText(text, kind)
	var images []string
	if kind == KindUser {
		images, text = splitAttachedImages(text)
	}
	if text == "" && len(images) == 0 {
		return nil
	}
	return []Entry{t.upsert(Entry{ID: fmt.Sprintf("%s:%d", entry.UUID, position), Kind: kind, Text: text, Images: images, Time: entry.Timestamp})}
}

func (t *Transcript) upsert(entry Entry) Entry {
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
	attachedImage  = regexp.MustCompile(`^(/\S*/clipboard-\d{4}-\d{2}-\d{2}-\d{6}-[0-9a-fA-F]{8}\.(?i:jpe?g|png|gif|webp))(?:\s+|$)`)
)

func IsAttachedImage(path string) bool {
	match := attachedImage.FindStringSubmatch(path)
	return match != nil && match[1] == path
}

func splitAttachedImages(text string) ([]string, string) {
	var images []string
	for {
		match := attachedImage.FindStringSubmatch(text)
		if match == nil {
			return images, text
		}
		images = append(images, match[1])
		text = text[len(match[0]):]
	}
}

func cleanText(text, kind string) (string, string) {
	text = strings.TrimSpace(systemReminder.ReplaceAllString(text, ""))
	if kind != KindUser {
		return text, kind
	}
	if match := commandName.FindStringSubmatch(text); match != nil {
		command := strings.TrimSpace(match[1])
		if args := commandArgs.FindStringSubmatch(text); args != nil && strings.TrimSpace(args[1]) != "" {
			command += " " + strings.TrimSpace(args[1])
		}
		return command, KindUser
	}
	if match := localOutput.FindStringSubmatch(text); match != nil {
		return strings.TrimSpace(match[2]), KindNotice
	}
	if strings.HasPrefix(text, "[Request interrupted") {
		return "Interrupted", KindNotice
	}
	return text, kind
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
		value = shortPath(value)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return Truncate(first, maxSummaryLength)
}

func shortPath(value string) string {
	dir, file := path.Split(value)
	parent := path.Base(strings.TrimSuffix(dir, "/"))
	if parent == "." || parent == "/" || parent == "" {
		return file
	}
	return parent + "/" + file
}

func Truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	count := 0
	for index := range text {
		if count == limit {
			return text[:index] + "…"
		}
		count++
	}
	return text
}
