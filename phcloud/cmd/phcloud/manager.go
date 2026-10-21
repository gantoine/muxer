package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gantoine/muxer/phcloud/internal/posthogcloud"
)

const (
	taskPageSize    = 50
	refreshInterval = 10 * time.Second
	actionTimeout   = time.Minute
	liveThrottle    = 250 * time.Millisecond
)

type view int

const (
	viewList view = iota
	viewSearch
	viewConfirmArchive
	viewNew
)

type tab int

const (
	tabActive tab = iota
	tabAll
)

type (
	tasksMsg   struct{ tasks []posthogcloud.Task }
	errMsg     struct{ err error }
	noticeMsg  struct{ text string }
	tickMsg    struct{}
	liveMsg    struct{}
	connectMsg struct{ err error }
)

type manager struct {
	paths  paths
	ctx    context.Context
	cancel context.CancelFunc
	cmux   *cmux

	connected bool
	config    posthogcloud.Config
	client    *posthogcloud.Client
	watcher   *watcher
	send      func(tea.Msg)

	view    view
	tab     tab
	tasks   []posthogcloud.Task
	loaded  bool
	cursor  int
	search  textinput.Model
	query   string
	notice  string
	failure string
	width   int
	height  int

	form     newRunForm
	showHelp bool
}

func runManager(paths paths) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &manager{paths: paths, ctx: ctx, cancel: cancel, cmux: newCmux(paths.workspaces)}
	m.search = textinput.New()
	m.search.Placeholder = "search runs"
	m.search.Prompt = "/ "
	program := tea.NewProgram(m, tea.WithAltScreen())
	m.send = program.Send
	m.connect()
	_, err := program.Run()
	if m.watcher != nil {
		m.watcher.stopAll()
	}
	return err
}

func (m *manager) connect() {
	config, ok, err := posthogcloud.LoadConfig(m.paths.config)
	if err != nil {
		m.failure = err.Error()
	}
	if !ok {
		m.connected = false
		return
	}
	if m.watcher != nil {
		m.watcher.stopAll()
	}
	m.connected, m.config = true, config
	m.client = posthogcloud.NewClient(config, nil)
	var pending atomic.Bool
	m.watcher = newWatcher(m.ctx, m.client, config, m.cmux, func() {
		if pending.CompareAndSwap(false, true) {
			time.AfterFunc(liveThrottle, func() {
				pending.Store(false)
				m.send(liveMsg{})
			})
		}
	})
}

func (m *manager) Init() tea.Cmd {
	return tea.Batch(tea.SetWindowTitle("Cloud runs"), m.load(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *manager) load() tea.Cmd {
	if !m.connected {
		return nil
	}
	client, query := m.client, m.query
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionTimeout)
		defer cancel()
		tasks, err := client.FindTasks(ctx, posthogcloud.TaskQuery{Limit: taskPageSize, Search: query})
		if err != nil {
			return errMsg{err}
		}
		return tasksMsg{tasks}
	}
}

func (m *manager) act(work func(ctx context.Context) (string, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionTimeout)
		defer cancel()
		text, err := work(ctx)
		if err != nil {
			return errMsg{err}
		}
		return noticeMsg{text}
	}
}

func (m *manager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.view == viewNew {
			m.form.resize(msg.Width)
		}
		return m, nil
	case tasksMsg:
		m.tasks, m.loaded, m.failure = msg.tasks, true, ""
		m.watcher.sync(msg.tasks)
		m.clampCursor()
		return m, nil
	case errMsg:
		m.failure, m.notice = msg.err.Error(), ""
		return m, nil
	case noticeMsg:
		m.notice, m.failure = msg.text, ""
		return m, m.load()
	case tickMsg:
		if !m.connected {
			m.connect()
		}
		return m, tea.Batch(m.load(), tick())
	case liveMsg:
		return m, nil
	case connectMsg:
		m.connect()
		if m.connected {
			m.notice = "Connected as " + m.config.Email + "."
		}
		return m, m.load()
	case repositoriesMsg, repositorySearchMsg, startedMsg:
		return m.updateForm(msg)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.view {
		case viewSearch:
			return m.updateSearch(msg)
		case viewConfirmArchive:
			return m.updateConfirm(msg)
		case viewNew:
			return m.updateForm(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *manager) updateList(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.connected {
		switch key.String() {
		case "c":
			m.failure = ""
			return m, tea.ExecProcess(exec.Command(m.cmux.self, "connect"), func(err error) tea.Msg {
				if err != nil {
					return errMsg{fmt.Errorf("connecting failed; run `phcloud connect` in a terminal to see why")}
				}
				return connectMsg{}
			})
		case "q":
			return m, tea.Quit
		}
		return m, nil
	}
	visible := m.visible()
	var selected *posthogcloud.Task
	if m.cursor < len(visible) {
		selected = &visible[m.cursor]
	}
	m.notice, m.failure = "", ""
	switch key.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(visible)-1, m.cursor+1)
	case "tab", "shift+tab":
		m.tab = 1 - m.tab
		m.clampCursor()
	case "/":
		m.view = viewSearch
		m.search.SetValue(m.query)
		return m, m.search.Focus()
	case "?":
		m.showHelp = !m.showHelp
	case "r":
		m.notice = "Refreshing…"
		return m, m.load()
	case "n":
		m.view = viewNew
		return m, m.form.open(m)
	case "T":
		return m, m.runSelf(connectMsg{}, "claude-token")
	}
	if selected == nil {
		return m, nil
	}
	task := *selected
	switch key.String() {
	case "enter", "o":
		return m, m.act(func(ctx context.Context) (string, error) {
			return "", m.cmux.open(ctx, task)
		})
	case "w":
		return m, m.act(func(context.Context) (string, error) {
			return "", exec.Command("open", m.webURL(task)).Run()
		})
	case "s":
		if task.LatestRun == nil {
			return m, nil
		}
		return m, m.act(func(ctx context.Context) (string, error) {
			return "Asked the agent to stop.", stopRun(ctx, m.client, task.ID, task.LatestRun.ID)
		})
	case "x":
		m.view = viewConfirmArchive
	case "a", "A", "d":
		if task.LatestRun == nil {
			return m, nil
		}
		mode := map[string]string{"a": answerOnce, "A": answerAlways, "d": answerDeny}[key.String()]
		follower := m.watcher.follower(task.LatestRun.ID)
		return m, m.act(func(ctx context.Context) (string, error) {
			return "Answered.", answerApproval(ctx, m.client, task.ID, task.LatestRun.ID, follower, mode)
		})
	}
	return m, nil
}

func (m *manager) runSelf(done tea.Msg, args ...string) tea.Cmd {
	command := exec.Command(m.cmux.self, args...)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		if err != nil {
			return errMsg{err}
		}
		return done
	})
}

func (m *manager) updateSearch(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.view = viewList
		m.search.Blur()
		return m, nil
	case "enter":
		m.view = viewList
		m.search.Blur()
		m.query = strings.TrimSpace(m.search.Value())
		m.tab, m.cursor = tabAll, 0
		return m, m.load()
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(key)
	return m, cmd
}

func (m *manager) updateConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.view = viewList
	visible := m.visible()
	if key.String() != "y" || m.cursor >= len(visible) {
		return m, nil
	}
	task := visible[m.cursor]
	return m, m.act(func(ctx context.Context) (string, error) {
		if task.LatestRun != nil {
			if !posthogcloud.Terminal(task.LatestRun.Status) {
				_ = stopRun(ctx, m.client, task.ID, task.LatestRun.ID)
			}
			m.cmux.close(ctx, task.LatestRun.ID)
		}
		return "Archived “" + taskTitle(task) + "”.", m.client.Archive(ctx, task.ID)
	})
}

func (m *manager) webURL(task posthogcloud.Task) string {
	return strings.TrimSuffix(m.config.Host, "/") + "/project/" + strconv.Itoa(m.config.ProjectID) + "/tasks/" + task.ID
}

func (m *manager) visible() []posthogcloud.Task {
	return m.visibleIn(m.tab)
}

func (m *manager) visibleIn(shown tab) []posthogcloud.Task {
	if shown == tabAll {
		return m.tasks
	}
	var active []posthogcloud.Task
	for _, task := range m.tasks {
		if task.LatestRun != nil && !posthogcloud.Terminal(task.LatestRun.Status) {
			active = append(active, task)
		}
	}
	return active
}

func (m *manager) clampCursor() {
	m.cursor = max(0, min(m.cursor, len(m.visible())-1))
}

func (m *manager) View() string {
	if m.view == viewNew {
		return m.form.view(m)
	}
	width := contentWidth(m.width)
	var b strings.Builder
	b.WriteString(titleStyle.Render("Cloud runs") + "\n")
	if !m.connected {
		b.WriteString("\n" + wrap("Connect PostHog with a personal API key to see and start cloud runs.", width) + "\n\n")
		b.WriteString(hints([]keyHint{{"c", "connect PostHog"}}, width) + "\n")
		if m.failure != "" {
			b.WriteString("\n" + alertStyle.Render(wrap(m.failure, width)) + "\n")
		}
		return frame(m.width, m.height, b.String(), hints([]keyHint{{"q", "quit"}}, width))
	}
	b.WriteString(m.tabs() + "\n")
	if m.query != "" {
		b.WriteString(dimStyle.Render(truncateWidth("matching “"+m.query+"”", width)) + "\n")
	}
	b.WriteString("\n")
	visible := m.visible()
	switch {
	case !m.loaded && m.failure == "":
		b.WriteString(dimStyle.Render("Loading…") + "\n")
	case m.loaded && len(visible) == 0 && m.tab == tabActive:
		b.WriteString("Nothing running.\n" + dimStyle.Render(wrap("Press n to start a run, or tab to see past ones.", width)) + "\n")
	case m.loaded && len(visible) == 0:
		b.WriteString(dimStyle.Render("No runs found.") + "\n")
	}
	b.WriteString(m.rows(visible, width))
	if m.cursor < len(visible) {
		b.WriteString(m.detail(visible[m.cursor], width))
	}
	return frame(m.width, m.height, b.String(), m.footer(width))
}

func (m *manager) tabs() string {
	active := len(m.visibleIn(tabActive))
	names := []string{"Active", "All"}
	if active > 0 {
		names[0] = "Active " + strconv.Itoa(active)
	}
	parts := make([]string, len(names))
	for i, name := range names {
		if tab(i) == m.tab {
			parts[i] = accentStyle.Bold(true).Underline(true).Render(name)
		} else {
			parts[i] = dimStyle.Render(name)
		}
	}
	return strings.Join(parts, "   ")
}

func (m *manager) rows(visible []posthogcloud.Task, width int) string {
	chrome := 12
	budget := max(3, m.height-chrome)
	start := 0
	if m.cursor >= budget-1 {
		start = m.cursor - budget + 2
	}
	var b strings.Builder
	used, next := 0, start
	for i := start; i < len(visible) && used < budget; i++ {
		next = i + 1
		task := visible[i]
		glyph, state := m.status(task)
		age := since(task.UpdatedAt)
		room := width - 4 - lipgloss.Width(age) - 1
		title := truncateWidth(taskTitle(task), room)
		gutter := "  "
		if i == m.cursor {
			gutter, title = barStyle.Render("▌ "), boldStyle.Render(title)
		}
		pad := max(1, width-2-2-lipgloss.Width(title)-lipgloss.Width(age))
		b.WriteString(gutter + glyph + " " + title + strings.Repeat(" ", pad) + dimStyle.Render(age) + "\n")
		used++
		if i == m.cursor {
			meta := state
			if task.Repository != "" {
				meta += dimStyle.Render(" · " + repositoryGroup(task.Repository))
			}
			b.WriteString(gutter + "  " + truncateWidth(meta, width-4) + "\n")
			used++
		}
	}
	if next < len(visible) {
		b.WriteString(dimStyle.Render("  ↓ more") + "\n")
	}
	return b.String()
}

func (m *manager) status(task posthogcloud.Task) (string, string) {
	if task.LatestRun == nil {
		return dimStyle.Render("·"), "no run"
	}
	status := task.LatestRun.Status
	if live, ok := m.watcher.live(task.LatestRun.ID); ok {
		status = live.status
		if !posthogcloud.Terminal(status) {
			switch live.state {
			case posthogcloud.StateWaiting:
				return alertStyle.Render("●"), alertStyle.Render("needs you")
			case posthogcloud.StateIdle:
				return accentStyle.Render("○"), accentStyle.Render("your turn")
			}
		}
	}
	switch status {
	case "completed":
		return goodStyle.Render("✓"), "done"
	case "failed":
		return alertStyle.Render("✗"), "failed"
	case "cancelled":
		return dimStyle.Render("■"), "stopped"
	case "queued", "not_started":
		return warnStyle.Render("◌"), "starting"
	}
	return warnStyle.Render("◐"), "working"
}

func (m *manager) detail(task posthogcloud.Task, width int) string {
	if task.LatestRun == nil {
		return ""
	}
	live, ok := m.watcher.live(task.LatestRun.ID)
	if !ok || len(live.pending) == 0 {
		return ""
	}
	inner := width - 4
	text := alertStyle.Bold(true).Render("Needs you") + "\n" + wrap(approvalSummary(live.pending[0]), inner) + "\n\n" +
		hints([]keyHint{{"a", "allow"}, {"A", "always"}, {"d", "deny"}}, inner)
	return cardStyle.Width(width-2).Render(text) + "\n"
}

func (m *manager) footer(width int) string {
	var status string
	switch {
	case m.view == viewSearch:
		status = m.search.View()
	case m.view == viewConfirmArchive:
		status = warnStyle.Render(wrap("Archive this run? Press y to confirm.", width))
	case m.failure != "":
		status = alertStyle.Render(wrap(m.failure, width))
	case m.notice != "":
		status = goodStyle.Render(wrap(m.notice, width))
	}
	keys := []keyHint{{"↵", "open"}, {"n", "new"}, {"/", "search"}, {"?", "more"}}
	if m.showHelp {
		keys = []keyHint{{"↵", "open"}, {"n", "new"}, {"s", "stop"}, {"x", "archive"}, {"w", "web"}, {"/", "search"}, {"tab", "view"}, {"r", "refresh"}, {"T", "Claude token"}, {"?", "less"}}
	}
	help := hints(keys, width)
	if status == "" {
		return help
	}
	return status + "\n\n" + help
}

func since(timestamp string) string {
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return ""
	}
	elapsed := time.Since(at)
	switch {
	case elapsed < time.Minute:
		return "now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	}
	return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
}
