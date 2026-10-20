package codex

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gantoine/muxer/relay/internal/chat"
)

const (
	toolShell = "Shell"
	toolPlan  = "Plan"
)

type line struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type payload struct {
	Type        string          `json:"type"`
	Item        json.RawMessage `json:"item"`
	Message     string          `json:"message"`
	Text        string          `json:"text"`
	LocalImages []string        `json:"local_images"`
	TurnID      string          `json:"turn_id"`
	Name        string          `json:"name"`
	Arguments   string          `json:"arguments"`
	Input       string          `json:"input"`
	CallID      string          `json:"call_id"`
	Output      json.RawMessage `json:"output"`
}

type item struct {
	Type             string                `json:"type"`
	ID               string                `json:"id"`
	Content          []content             `json:"content"`
	SummaryText      []string              `json:"summary_text"`
	Command          []string              `json:"command"`
	AggregatedOutput string                `json:"aggregated_output"`
	ExitCode         *int                  `json:"exit_code"`
	Status           string                `json:"status"`
	Changes          map[string]fileChange `json:"changes"`
}

type content struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	Path         string `json:"path"`
	TextElements []struct {
		Placeholder string `json:"placeholder"`
	} `json:"text_elements"`
}

type fileChange struct {
	Type        string `json:"type"`
	UnifiedDiff string `json:"unified_diff"`
}

type Transcript struct {
	entries    []chat.Entry
	index      map[string]int
	seen       map[string]int
	typedItems bool
}

func NewTranscript() *Transcript {
	return &Transcript{index: map[string]int{}, seen: map[string]int{}}
}

func (t *Transcript) Entries() []chat.Entry {
	return append([]chat.Entry(nil), t.entries...)
}

func (t *Transcript) Feed(raw []byte) []chat.Entry {
	var entry line
	if err := json.Unmarshal(raw, &entry); err != nil || len(entry.Payload) == 0 {
		return nil
	}
	if entry.Type != "event_msg" && entry.Type != "response_item" {
		return nil
	}
	var p payload
	if err := json.Unmarshal(entry.Payload, &p); err != nil {
		return nil
	}
	if p.Type == "item_completed" {
		t.typedItems = true
		var it item
		if err := json.Unmarshal(p.Item, &it); err != nil || it.ID == "" {
			return nil
		}
		return t.typedItem(it, entry.Timestamp)
	}
	if p.Type == "turn_aborted" {
		return t.add(chat.Entry{ID: t.lineID("aborted", p.TurnID, entry.Timestamp), Kind: chat.KindNotice, Text: "Interrupted", Time: entry.Timestamp})
	}
	if t.typedItems {
		return nil
	}
	if entry.Type == "event_msg" {
		return t.event(p, entry.Timestamp)
	}
	return t.call(p, entry.Timestamp)
}

func (t *Transcript) typedItem(it item, timestamp string) []chat.Entry {
	switch it.Type {
	case "UserMessage":
		var parts, images []string
		for _, c := range it.Content {
			switch c.Type {
			case "text":
				text := c.Text
				for _, element := range c.TextElements {
					if element.Placeholder != "" {
						text = strings.ReplaceAll(text, element.Placeholder, "")
					}
				}
				parts = append(parts, text)
			case "local_image":
				if chat.IsAttachedImage(c.Path) {
					images = append(images, c.Path)
				}
			}
		}
		return t.user(it.ID, strings.Join(parts, "\n"), images, timestamp)
	case "AgentMessage":
		var parts []string
		for _, c := range it.Content {
			if strings.EqualFold(c.Type, "text") {
				parts = append(parts, c.Text)
			}
		}
		return t.text(it.ID, chat.KindAssistant, strings.Join(parts, "\n"), timestamp)
	case "Reasoning":
		return t.thinking(it.ID, strings.Join(it.SummaryText, "\n\n"), timestamp)
	case "CommandExecution":
		status := toolStatus(it.Status)
		if status == chat.ToolDone && it.ExitCode != nil && *it.ExitCode != 0 {
			status = chat.ToolFailed
		}
		return t.add(chat.Entry{ID: it.ID, Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{
			Name:    toolShell,
			Summary: summary(commandText(it.Command)),
			Status:  status,
			Output:  chat.Truncate(strings.TrimSpace(it.AggregatedOutput), chat.MaxOutputLength),
		}})
	case "FileChange":
		paths := make([]string, 0, len(it.Changes))
		for path := range it.Changes {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		var changed []chat.Entry
		for i, path := range paths {
			change := it.Changes[path]
			changed = append(changed, t.add(chat.Entry{ID: fmt.Sprintf("%s:%d", it.ID, i), Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{
				Name:    fileToolName(change.Type),
				Summary: chat.ShortPath(path),
				Status:  toolStatus(it.Status),
				Diff:    chat.UnifiedDiffHunks(change.UnifiedDiff),
			}})...)
		}
		return changed
	}
	return nil
}

func (t *Transcript) event(p payload, timestamp string) []chat.Entry {
	switch p.Type {
	case "user_message":
		var images []string
		for _, path := range p.LocalImages {
			if chat.IsAttachedImage(path) {
				images = append(images, path)
			}
		}
		return t.user(t.lineID("user", "", timestamp), imagePlaceholder.ReplaceAllString(p.Message, ""), images, timestamp)
	case "agent_message":
		return t.text(t.lineID("assistant", "", timestamp), chat.KindAssistant, p.Message, timestamp)
	case "agent_reasoning":
		return t.thinking(t.lineID("thinking", "", timestamp), p.Text, timestamp)
	}
	return nil
}

func (t *Transcript) call(p payload, timestamp string) []chat.Entry {
	switch p.Type {
	case "function_call":
		switch p.Name {
		case "exec_command", "shell":
			return t.add(chat.Entry{ID: p.CallID, Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{
				Name:    toolShell,
				Summary: summary(shellArguments(p.Arguments)),
				Status:  chat.ToolRunning,
			}})
		case "update_plan":
			return t.add(chat.Entry{ID: p.CallID, Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{
				Name:   toolPlan,
				Status: chat.ToolRunning,
				Todos:  planTodos(p.Arguments),
			}})
		}
	case "custom_tool_call":
		if p.Name == "apply_patch" {
			name, path := patchTarget(p.Input)
			return t.add(chat.Entry{ID: p.CallID, Kind: chat.KindTool, Time: timestamp, Tool: &chat.Tool{
				Name:    name,
				Summary: chat.ShortPath(path),
				Status:  chat.ToolRunning,
			}})
		}
	case "function_call_output", "custom_tool_call_output":
		position, ok := t.index[p.CallID]
		if !ok || p.CallID == "" {
			return nil
		}
		updated := t.entries[position]
		tool := *updated.Tool
		output, failed := callOutput(p.Output)
		tool.Status = chat.ToolDone
		if failed {
			tool.Status = chat.ToolFailed
		}
		if tool.Name != toolPlan {
			tool.Output = chat.Truncate(output, chat.MaxOutputLength)
		}
		updated.Tool = &tool
		return t.add(updated)
	}
	return nil
}

var (
	imagePlaceholder = regexp.MustCompile(`\[Image #\d+\]`)
	exitCodeLine     = regexp.MustCompile(`(?m)^Process exited with code (\d+)$`)
	patchFileLine    = regexp.MustCompile(`(?m)^\*\*\* (Update|Add|Delete) File: (.+)$`)
)

func (t *Transcript) user(id, text string, images []string, timestamp string) []chat.Entry {
	text = strings.TrimSpace(text)
	if text == "" && len(images) == 0 {
		return nil
	}
	return t.add(chat.Entry{ID: id, Kind: chat.KindUser, Text: text, Images: images, Time: timestamp})
}

func (t *Transcript) text(id, kind, text, timestamp string) []chat.Entry {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return t.add(chat.Entry{ID: id, Kind: kind, Text: text, Time: timestamp})
}

func (t *Transcript) thinking(id, text, timestamp string) []chat.Entry {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return t.add(chat.Entry{ID: id, Kind: chat.KindThinking, Text: chat.Truncate(text, chat.MaxThinkingLength), Time: timestamp})
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

func (t *Transcript) lineID(kind, key, timestamp string) string {
	base := kind + ":" + key + ":" + timestamp
	t.seen[base]++
	return fmt.Sprintf("%s:%d", base, t.seen[base])
}

func toolStatus(status string) string {
	switch status {
	case "completed":
		return chat.ToolDone
	case "inProgress", "in_progress":
		return chat.ToolRunning
	}
	return chat.ToolFailed
}

func fileToolName(changeType string) string {
	switch changeType {
	case "add":
		return "Write"
	case "delete":
		return "Delete"
	}
	return "Edit"
}

func commandText(command []string) string {
	if len(command) == 3 && (command[1] == "-lc" || command[1] == "-c") {
		return command[2]
	}
	return strings.Join(command, " ")
}

func shellArguments(raw string) string {
	var arguments struct {
		Cmd     string   `json:"cmd"`
		Command []string `json:"command"`
	}
	if json.Unmarshal([]byte(raw), &arguments) != nil {
		return ""
	}
	if arguments.Cmd != "" {
		return arguments.Cmd
	}
	return commandText(arguments.Command)
}

func summary(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return chat.Truncate(first, chat.MaxSummaryLength)
}

func planTodos(raw string) []chat.Todo {
	var arguments struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal([]byte(raw), &arguments) != nil {
		return nil
	}
	var todos []chat.Todo
	for _, step := range arguments.Plan {
		if text := strings.TrimSpace(step.Step); text != "" {
			todos = append(todos, chat.Todo{Content: chat.Truncate(text, chat.MaxSummaryLength), Status: step.Status})
		}
	}
	return todos
}

func patchTarget(patch string) (name, path string) {
	match := patchFileLine.FindStringSubmatch(patch)
	if match == nil {
		return "Edit", ""
	}
	return fileToolName(strings.ToLower(match[1])), strings.TrimSpace(match[2])
}

func callOutput(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	var wrapped struct {
		Output   string `json:"output"`
		Metadata struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(text), &wrapped) == nil && wrapped.Metadata.ExitCode != nil {
		return strings.TrimSpace(wrapped.Output), *wrapped.Metadata.ExitCode != 0
	}
	failed := false
	if match := exitCodeLine.FindStringSubmatch(text); match != nil {
		code, _ := strconv.Atoi(match[1])
		failed = code != 0
	}
	if _, output, ok := strings.Cut(text, "\nOutput:\n"); ok {
		text = output
	}
	return strings.TrimSpace(text), failed
}
