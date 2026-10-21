package main

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/gantoine/phcloud/internal/chat"
	"github.com/gantoine/phcloud/internal/posthogcloud"
)

const (
	indent           = "  "
	maxComposerLines = 6
	maxOutputLines   = 10
	maxDiffLines     = 24
	maxPlanLines     = 12
	spinInterval     = 120 * time.Millisecond
	maxReadingWidth  = 100
)

var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

type (
	changedMsg struct{}
	taskMsg    struct{ task posthogcloud.Task }
	sentMsg    struct {
		text string
		err  error
	}
	spinMsg struct{}
)

type renderedMarkdown struct {
	text  string
	width int
	out   string
}

type runView struct {
	ctx    context.Context
	client *posthogcloud.Client
	source *directSource
	config posthogcloud.Config
	dark   bool

	task      posthogcloud.Task
	width     int
	height    int
	viewport  viewport.Model
	composer  textarea.Model
	showTools bool
	notice    string
	failure   string
	frame     int

	markdown      *glamour.TermRenderer
	markdownWidth int
	rendered      map[string]renderedMarkdown
}

func newRunView(ctx context.Context, client *posthogcloud.Client, source *directSource, config posthogcloud.Config, dark bool) *runView {
	composer := textarea.New()
	composer.Placeholder = "Reply to the agent"
	composer.Prompt = "› "
	composer.ShowLineNumbers = false
	composer.CharLimit = 0
	composer.MaxHeight = maxComposerLines
	composer.SetHeight(1)
	composer.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	for _, style := range []*textarea.Style{&composer.FocusedStyle, &composer.BlurredStyle} {
		style.CursorLine = lipgloss.NewStyle()
		style.Base = lipgloss.NewStyle()
		style.Placeholder = dimStyle
		style.Prompt = barStyle
	}
	composer.Focus()
	return &runView{ctx: ctx, client: client, source: source, config: config, dark: dark, composer: composer, viewport: viewport.New(0, 0), rendered: map[string]renderedMarkdown{}}
}

func spin() tea.Cmd {
	return tea.Tick(spinInterval, func(time.Time) tea.Msg { return spinMsg{} })
}

func (v *runView) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, spin())
}

func (v *runView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.composer.SetWidth(v.contentWidth())
		v.refresh()
		return v, nil
	case changedMsg:
		v.refresh()
		return v, nil
	case taskMsg:
		v.task = msg.task
		return v, nil
	case noticeMsg:
		v.notice = msg.text
		return v, nil
	case errMsg:
		v.failure = msg.err.Error()
		return v, nil
	case sentMsg:
		if msg.err != nil {
			v.failure = msg.err.Error()
			if strings.TrimSpace(v.composer.Value()) == "" && msg.text != "" {
				v.composer.SetValue(msg.text)
			}
		}
		v.refresh()
		return v, nil
	case spinMsg:
		v.frame++
		if v.source.snapshot().state == posthogcloud.StateWorking {
			v.refresh()
		}
		return v, spin()
	case tea.MouseMsg:
		var cmd tea.Cmd
		v.viewport, cmd = v.viewport.Update(msg)
		return v, cmd
	case tea.KeyMsg:
		return v.key(msg)
	}
	return v, nil
}

func (v *runView) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v.notice, v.failure = "", ""
	empty := strings.TrimSpace(v.composer.Value()) == ""
	snapshot := v.source.snapshot()
	switch msg.String() {
	case "ctrl+c":
		return v, tea.Quit
	case "esc":
		if !empty {
			v.composer.Reset()
			v.layout()
			return v, nil
		}
		if snapshot.state == posthogcloud.StateWorking || snapshot.state == posthogcloud.StateWaiting {
			v.notice = "Stopping…"
			return v, v.perform(viewerAction{kind: actionStop}, "")
		}
		return v, nil
	case "ctrl+o":
		v.showTools = !v.showTools
		v.refresh()
		return v, nil
	case "pgup", "ctrl+u":
		v.viewport.HalfPageUp()
		return v, nil
	case "pgdown", "ctrl+d":
		v.viewport.HalfPageDown()
		return v, nil
	case "shift+up":
		v.viewport.ScrollUp(1)
		return v, nil
	case "shift+down":
		v.viewport.ScrollDown(1)
		return v, nil
	case "end":
		if empty {
			v.viewport.GotoBottom()
			return v, nil
		}
	case "enter":
		text := strings.TrimSpace(v.composer.Value())
		if text == "" {
			return v, nil
		}
		action, err := parseViewerLine(text)
		if err != nil {
			v.failure = err.Error()
			return v, nil
		}
		v.composer.Reset()
		v.layout()
		v.viewport.GotoBottom()
		return v, v.perform(action, text)
	}
	if empty && len(snapshot.pending) > 0 {
		if choice, err := strconv.Atoi(msg.String()); err == nil && choice >= 1 && choice <= len(snapshot.pending[0].Options) {
			option := snapshot.pending[0].Options[choice-1]
			v.notice = "Answered: " + option.Name
			return v, v.perform(viewerAction{kind: actionAnswer, option: option.OptionID}, "")
		}
	}
	var cmd tea.Cmd
	v.composer, cmd = v.composer.Update(msg)
	v.layout()
	return v, cmd
}

func (v *runView) perform(action viewerAction, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(v.ctx, actionTimeout)
		defer cancel()
		return sentMsg{text: text, err: v.source.act(ctx, action)}
	}
}

func (v *runView) layout() {
	v.composer.SetHeight(min(maxComposerLines, max(1, v.composer.LineCount())))
	chrome := lipgloss.Height(v.header()) + lipgloss.Height(v.bottom())
	v.viewport.Width = v.width
	v.viewport.Height = max(1, v.height-chrome)
}

func (v *runView) refresh() {
	if v.width == 0 {
		return
	}
	following := v.viewport.AtBottom() || v.viewport.TotalLineCount() == 0
	v.layout()
	v.viewport.SetContent(v.transcript())
	if following {
		v.viewport.GotoBottom()
	}
}

func (v *runView) View() string {
	if v.width == 0 {
		return ""
	}
	return v.header() + "\n" + v.viewport.View() + "\n" + v.bottom()
}

func (v *runView) header() string {
	snapshot := v.source.snapshot()
	width := v.contentWidth()
	title := taskTitle(v.task)
	if v.task.ID == "" {
		title = "Cloud run"
		for _, entry := range snapshot.entries {
			if entry.Kind == chat.KindUser {
				first, _, _ := strings.Cut(strings.TrimSpace(entry.Text), "\n")
				title = first
				break
			}
		}
	}
	glyph, _ := v.state(snapshot)
	var meta []string
	if v.task.Repository != "" {
		meta = append(meta, v.task.Repository)
	}
	if snapshot.run.Branch != "" {
		meta = append(meta, snapshot.run.Branch)
	}
	if url := snapshot.run.PullRequestURL(); url != "" {
		meta = append(meta, "PR #"+path.Base(url))
	}
	if snapshot.run.Model != "" {
		meta = append(meta, snapshot.run.Model)
	}
	line := "\n" + indent + glyph + " " + boldStyle.Render(truncateWidth(title, width-2))
	if len(meta) > 0 {
		line += "\n" + indent + dimStyle.Render(truncateWidth(strings.Join(meta, " · "), width))
	}
	return line + "\n" + indent + dimStyle.Render(strings.Repeat("─", max(1, width)))
}

func (v *runView) state(snapshot viewSnapshot) (string, string) {
	if !snapshot.ready {
		return dimStyle.Render("·"), dimStyle.Render("Connecting…")
	}
	switch snapshot.run.Status {
	case "completed":
		done := "Done"
		if url := snapshot.run.PullRequestURL(); url != "" {
			done += " · " + url
		}
		return goodStyle.Render("✓"), goodStyle.Render(done)
	case "failed":
		return alertStyle.Render("✗"), alertStyle.Render("Failed: " + cmp.Or(snapshot.run.ErrorMessage, "the run stopped with an error"))
	case "cancelled":
		return dimStyle.Render("■"), dimStyle.Render("Stopped · reply to bring it back")
	case "queued", "not_started":
		return warnStyle.Render(v.spinner()), warnStyle.Render("Starting the sandbox…")
	}
	switch snapshot.state {
	case posthogcloud.StateWaiting:
		return alertStyle.Render("●"), alertStyle.Render("Waiting for you")
	case posthogcloud.StateIdle:
		return accentStyle.Render("○"), accentStyle.Render("Your turn")
	}
	working := "Working"
	if started, ok := lastPrompt(snapshot.entries); ok {
		working += " · " + elapsed(time.Since(started))
	}
	return warnStyle.Render(v.spinner()), warnStyle.Render(working)
}

func (v *runView) contentWidth() int {
	return max(16, min(maxReadingWidth, v.width-2*len(indent)))
}

func (v *runView) spinner() string {
	return spinnerFrames[v.frame%len(spinnerFrames)]
}

func (v *runView) bottom() string {
	snapshot := v.source.snapshot()
	width := v.contentWidth()
	var b strings.Builder
	if sheet := v.sheet(snapshot, width); sheet != "" {
		b.WriteString(sheet + "\n")
	}
	_, status := v.state(snapshot)
	switch {
	case v.failure != "":
		status = alertStyle.Render(v.failure)
	case v.notice != "":
		status = dimStyle.Render(v.notice)
	}
	b.WriteString(indent + truncateWidth(status, width) + "\n")
	keys := []keyHint{{"esc", "stop"}, {"^o", "tools"}, {"^c", "close"}}
	if snapshot.state != posthogcloud.StateWorking && snapshot.state != posthogcloud.StateWaiting {
		keys = []keyHint{{"^o", "tools"}, {"^c", "close"}}
	}
	hint := hints(keys, width)
	if lipgloss.Height(hint) == 1 && lipgloss.Width(hint)+4 < width {
		rule := strings.Repeat("─", width-lipgloss.Width(hint)-1)
		b.WriteString(indent + dimStyle.Render(rule) + " " + hint + "\n")
	} else {
		b.WriteString(indent + dimStyle.Render(strings.Repeat("─", max(1, width))) + "\n")
	}
	b.WriteString(indentLines(v.composer.View(), indent))
	return b.String()
}

func (v *runView) sheet(snapshot viewSnapshot, width int) string {
	if len(snapshot.pending) == 0 {
		return ""
	}
	approval := snapshot.pending[0]
	inner := width - 4
	var b strings.Builder
	b.WriteString(alertStyle.Bold(true).Render("Needs you") + "\n")
	switch {
	case approval.Kind == posthogcloud.ApprovalPermission:
		b.WriteString(wrap(cmp.Or(approval.Title, approval.Tool, "Allow a tool"), inner) + "\n")
		if approval.Detail != "" {
			b.WriteString(dimStyle.Render(clampLines(wrap(approval.Detail, inner), 4)) + "\n")
		}
	default:
		b.WriteString(wrap(approvalSummary(approval), inner) + "\n")
		if approval.Plan != "" {
			b.WriteString("\n" + dimStyle.Render(clampLines(wrap(approval.Plan, inner), maxPlanLines)) + "\n")
		}
	}
	var options []keyHint
	for i, option := range approval.Options {
		options = append(options, keyHint{strconv.Itoa(i + 1), option.Name})
	}
	if len(options) > 0 {
		b.WriteString("\n" + hints(options, inner))
	}
	return indentLines(cardStyle.Width(width-2).Render(strings.TrimRight(b.String(), "\n")), indent)
}

func (v *runView) transcript() string {
	snapshot := v.source.snapshot()
	width := v.contentWidth()
	if !snapshot.ready {
		return "\n" + indent + dimStyle.Render("Loading the run…")
	}
	var blocks []string
	var tools []chat.Entry
	flush := func() {
		if len(tools) > 0 {
			blocks = append(blocks, v.toolBlock(tools, width))
			tools = nil
		}
	}
	for _, entry := range snapshot.entries {
		if entry.Kind == chat.KindTool && entry.Tool != nil {
			tools = append(tools, entry)
			continue
		}
		flush()
		if block := v.entryBlock(entry, width); block != "" {
			blocks = append(blocks, block)
		}
	}
	flush()
	if footer := turnFooter(snapshot); footer != "" {
		blocks = append(blocks, indent+dimStyle.Render(footer))
	}
	return "\n" + strings.Join(blocks, "\n\n") + "\n"
}

func (v *runView) entryBlock(entry chat.Entry, width int) string {
	switch entry.Kind {
	case chat.KindUser:
		text := wrap(strings.TrimSpace(entry.Text), width-2)
		if len(entry.Images) > 0 {
			text += "\n" + dimStyle.Render(plural(len(entry.Images), "image", "images")+" attached")
		}
		return indentLines(text, indent+barStyle.Render("┃ "))
	case chat.KindAssistant:
		return v.renderMarkdown(entry.ID, entry.Text, width)
	case chat.KindThinking:
		if v.showTools {
			return indentLines(dimStyle.Italic(true).Render(wrap(strings.TrimSpace(entry.Text), width)), indent)
		}
		first, _, _ := strings.Cut(strings.TrimSpace(entry.Text), "\n")
		return indent + dimStyle.Italic(true).Render(truncateWidth("✻ "+first, width))
	case chat.KindNotice:
		return indentLines(dimStyle.Render(wrap("· "+entry.Text, width)), indent)
	}
	return ""
}

func (v *runView) toolBlock(tools []chat.Entry, width int) string {
	if !v.showTools && len(tools) > 1 {
		failed, running := 0, 0
		for _, entry := range tools {
			switch entry.Tool.Status {
			case chat.ToolFailed:
				failed++
			case chat.ToolRunning:
				running++
			}
		}
		glyph := dimStyle.Render("▸")
		if running > 0 {
			glyph = warnStyle.Render(v.spinner())
		}
		line := glyph + " " + dimStyle.Render("Ran "+plural(len(tools), "tool", "tools"))
		if failed > 0 {
			line += dimStyle.Render(" · ") + alertStyle.Render(strconv.Itoa(failed)+" failed")
		}
		return indent + line
	}
	var lines []string
	for _, entry := range tools {
		lines = append(lines, v.toolLine(entry.Tool, width))
		if v.showTools {
			if detail := toolDetail(entry.Tool, width-4); detail != "" {
				lines = append(lines, indentLines(detail, indent+"    "))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (v *runView) toolLine(tool *chat.Tool, width int) string {
	glyph := goodStyle.Render("✓")
	switch tool.Status {
	case chat.ToolRunning:
		glyph = warnStyle.Render(v.spinner())
	case chat.ToolFailed:
		glyph = alertStyle.Render("✗")
	}
	name := boldStyle.Render(tool.Name)
	room := width - 2 - lipgloss.Width(tool.Name) - 1
	summary := ""
	if tool.Summary != "" && room > 4 {
		summary = " " + dimStyle.Render(truncateWidth(tool.Summary, room))
	}
	return indent + glyph + " " + name + summary
}

func toolDetail(tool *chat.Tool, width int) string {
	var parts []string
	if len(tool.Diff) > 0 {
		var lines []string
		for _, hunk := range tool.Diff {
			for _, line := range hunk.Lines {
				styled := dimStyle
				switch {
				case strings.HasPrefix(line, "+"):
					styled = goodStyle
				case strings.HasPrefix(line, "-"):
					styled = alertStyle
				}
				lines = append(lines, styled.Render(truncateWidth(line, width)))
			}
		}
		parts = append(parts, clampLines(strings.Join(lines, "\n"), maxDiffLines))
	}
	for _, todo := range tool.Todos {
		mark := "○"
		switch todo.Status {
		case "completed":
			mark = "✓"
		case "in_progress":
			mark = "◐"
		}
		parts = append(parts, dimStyle.Render(truncateWidth(mark+" "+todo.Content, width)))
	}
	if output := strings.TrimSpace(tool.Output); output != "" && len(tool.Diff) == 0 {
		parts = append(parts, dimStyle.Render(clampLines(wrap(output, width), maxOutputLines)))
	}
	return strings.Join(parts, "\n")
}

func (v *runView) renderMarkdown(id, text string, width int) string {
	if cached, ok := v.rendered[id]; ok && cached.text == text && cached.width == width {
		return cached.out
	}
	if v.markdown == nil || v.markdownWidth != width {
		style := "light"
		if v.dark {
			style = "dark"
		}
		renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
		if err != nil {
			return indentLines(wrap(text, width), indent)
		}
		v.markdown, v.markdownWidth = renderer, width
	}
	out, err := v.markdown.Render(text)
	if err != nil {
		out = indentLines(wrap(text, width), indent)
	}
	out = strings.Trim(out, "\n")
	v.rendered[id] = renderedMarkdown{text: text, width: width, out: out}
	return out
}

func turnFooter(snapshot viewSnapshot) string {
	if len(snapshot.entries) == 0 || (snapshot.state != posthogcloud.StateIdle && !snapshot.run.Terminal()) {
		return ""
	}
	started, ok := lastPrompt(snapshot.entries)
	if !ok {
		return ""
	}
	finished, ok := parseTime(snapshot.entries[len(snapshot.entries)-1].Time)
	if !ok || finished.Before(started) {
		return ""
	}
	return "※ Worked for " + elapsed(finished.Sub(started)) + " · done " + finished.Local().Format("3:04 PM")
}

func lastPrompt(entries []chat.Entry) (time.Time, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == chat.KindUser {
			return parseTime(entries[i].Time)
		}
	}
	return time.Time{}, false
}

func parseTime(value string) (time.Time, bool) {
	at, err := time.Parse(time.RFC3339Nano, value)
	return at, err == nil
}

func elapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func clampLines(text string, limit int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= limit {
		return text
	}
	return strings.Join(lines[:limit], "\n") + "\n… " + plural(len(lines)-limit, "more line", "more lines")
}

func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
