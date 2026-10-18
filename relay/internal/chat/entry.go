package chat

import (
	"path"
	"regexp"
	"strings"
)

const (
	MaxSummaryLength  = 200
	MaxOutputLength   = 2000
	MaxThinkingLength = 8000
	MaxDiffLines      = 400
	maxDiffLineLength = 300
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
	Name    string   `json:"name"`
	Summary string   `json:"summary,omitempty"`
	Status  string   `json:"status"`
	Output  string   `json:"output,omitempty"`
	Images  []string `json:"images,omitempty"`
	Diff    []Hunk   `json:"diff,omitempty"`
	Todos   []Todo   `json:"todos,omitempty"`
}

type Todo struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

type Hunk struct {
	OldStart int      `json:"old_start"`
	NewStart int      `json:"new_start"`
	Lines    []string `json:"lines"`
}

const (
	KindUser      = "user"
	KindAssistant = "assistant"
	KindTool      = "tool"
	KindNotice    = "notice"
	KindThinking  = "thinking"

	ToolRunning = "running"
	ToolDone    = "done"
	ToolFailed  = "failed"
)

func CapHunks(hunks []Hunk) []Hunk {
	budget := MaxDiffLines
	var capped []Hunk
	for _, hunk := range hunks {
		if budget == 0 {
			break
		}
		lines := hunk.Lines
		if len(lines) > budget {
			lines = lines[:budget]
		}
		budget -= len(lines)
		shortened := make([]string, len(lines))
		for i, text := range lines {
			shortened[i] = Truncate(text, maxDiffLineLength)
		}
		capped = append(capped, Hunk{OldStart: hunk.OldStart, NewStart: hunk.NewStart, Lines: shortened})
	}
	return capped
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

var AttachedImage = regexp.MustCompile(`^(/\S*/clipboard-\d{4}-\d{2}-\d{2}-\d{6}-[0-9a-fA-F]{8}\.(?i:jpe?g|png|gif|webp))(?:\s+|$)`)

func IsAttachedImage(path string) bool {
	match := AttachedImage.FindStringSubmatch(path)
	return match != nil && match[1] == path
}

func ShortPath(value string) string {
	dir, file := path.Split(value)
	parent := path.Base(strings.TrimSuffix(dir, "/"))
	if parent == "." || parent == "/" || parent == "" {
		return file
	}
	return parent + "/" + file
}
