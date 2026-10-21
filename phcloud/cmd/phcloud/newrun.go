package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gantoine/muxer/phcloud/internal/posthogcloud"
)

const (
	repositoryDebounce = 300 * time.Millisecond
	maxSuggestions     = 6
)

const (
	fieldPrompt = iota
	fieldRepository
	fieldAgent
	fieldPlan
	fieldCount
)

type (
	repositoriesMsg struct {
		query        string
		repositories []posthogcloud.Repository
		err          error
	}
	repositorySearchMsg struct{ query string }
	startedMsg          struct {
		task posthogcloud.Task
		err  error
	}
)

type newRunForm struct {
	field        int
	repository   textinput.Model
	repositories []posthogcloud.Repository
	loading      bool
	pick         int
	chosen       *posthogcloud.Repository
	agent        string
	claudePlan   bool
	prompt       textarea.Model
	starting     bool
	failure      string
}

type startPrefs struct {
	Repository *posthogcloud.Repository `json:"repository,omitempty"`
	Agent      string                   `json:"agent,omitempty"`
	ClaudePlan bool                     `json:"claude_plan"`
}

func loadPrefs(path string) startPrefs {
	var prefs startPrefs
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &prefs)
	}
	return prefs
}

func savePrefs(path string, prefs startPrefs) {
	if raw, err := json.MarshalIndent(prefs, "", "  "); err == nil {
		_ = os.WriteFile(path, append(raw, '\n'), 0o600)
	}
}

func (f *newRunForm) open(m *manager) tea.Cmd {
	prefs := loadPrefs(m.paths.prefs)
	*f = newRunForm{agent: cmp.Or(prefs.Agent, posthogcloud.AgentClaude), claudePlan: prefs.ClaudePlan || (prefs.Agent == "" && m.config.ClaudeToken != "")}
	f.repository = textinput.New()
	f.repository.Placeholder = "owner/name"
	f.repository.Prompt = ""
	f.prompt = textarea.New()
	f.prompt.Placeholder = "What should the agent do?"
	f.prompt.ShowLineNumbers = false
	f.prompt.SetHeight(4)
	f.prompt.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	f.resize(m.width)
	f.loading = true
	f.field = fieldRepository
	if prefs.Repository != nil {
		f.chosen = prefs.Repository
		f.repository.SetValue(prefs.Repository.Name)
		f.field = fieldPrompt
	}
	return tea.Batch(f.focus(), m.findRepositories(""))
}

func (f *newRunForm) focus() tea.Cmd {
	f.repository.Blur()
	f.prompt.Blur()
	switch f.field {
	case fieldRepository:
		return f.repository.Focus()
	case fieldPrompt:
		return f.prompt.Focus()
	}
	return nil
}

func (f *newRunForm) resize(width int) {
	inner := contentWidth(width)
	f.repository.Width = max(8, inner-3)
	f.prompt.SetWidth(inner)
}

func (m *manager) findRepositories(query string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, actionTimeout)
		defer cancel()
		repositories, err := client.Repositories(ctx, query)
		return repositoriesMsg{query: query, repositories: repositories, err: err}
	}
}

func (f *newRunForm) planAvailable() bool {
	return f.agent == posthogcloud.AgentClaude
}

func (f *newRunForm) move(step int) tea.Cmd {
	f.field = (f.field + step + fieldCount) % fieldCount
	if f.field == fieldPlan && !f.planAvailable() {
		f.field = (f.field + step + fieldCount) % fieldCount
	}
	return f.focus()
}

func (m *manager) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.form
	switch msg := msg.(type) {
	case repositoriesMsg:
		if msg.query != strings.TrimSpace(f.repository.Value()) && f.chosen == nil {
			return m, nil
		}
		f.loading = false
		if msg.err != nil {
			f.failure = msg.err.Error()
			return m, nil
		}
		f.repositories, f.pick, f.failure = msg.repositories, 0, ""
		return m, nil
	case repositorySearchMsg:
		if msg.query != strings.TrimSpace(f.repository.Value()) {
			return m, nil
		}
		f.loading = true
		return m, m.findRepositories(msg.query)
	case startedMsg:
		f.starting = false
		if msg.err != nil {
			f.failure = msg.err.Error()
			return m, nil
		}
		savePrefs(m.paths.prefs, startPrefs{Repository: f.chosen, Agent: f.agent, ClaudePlan: f.claudePlan})
		m.view = viewList
		m.tab, m.cursor = tabActive, 0
		m.notice = "Started “" + taskTitle(msg.task) + "”."
		task := msg.task
		m.watcher.sync(append([]posthogcloud.Task{task}, m.tasks...))
		return m, tea.Batch(m.load(), m.act(func(ctx context.Context) (string, error) {
			return m.notice, m.cmux.open(ctx, task)
		}))
	case tea.KeyMsg:
		return m.formKey(msg)
	}
	return m, nil
}

func (m *manager) formKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.form
	if f.starting {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.view = viewList
		return m, nil
	case "tab":
		if f.field == fieldRepository && f.chosen == nil {
			f.choose()
		}
		return m, f.move(1)
	case "shift+tab":
		return m, f.move(-1)
	}
	switch f.field {
	case fieldRepository:
		switch msg.String() {
		case "up":
			f.pick = max(0, f.pick-1)
			return m, nil
		case "down":
			f.pick = min(len(f.suggestions())-1, f.pick+1)
			return m, nil
		case "enter":
			if f.choose() {
				return m, f.move(1)
			}
			return m, nil
		}
		before := f.repository.Value()
		var cmd tea.Cmd
		f.repository, cmd = f.repository.Update(msg)
		if f.repository.Value() == before {
			return m, cmd
		}
		f.chosen = nil
		query := strings.TrimSpace(f.repository.Value())
		return m, tea.Batch(cmd, tea.Tick(repositoryDebounce, func(time.Time) tea.Msg { return repositorySearchMsg{query} }))
	case fieldAgent:
		switch msg.String() {
		case "left", "right", " ", "h", "l":
			f.agent = map[string]string{posthogcloud.AgentClaude: posthogcloud.AgentCodex, posthogcloud.AgentCodex: posthogcloud.AgentClaude}[f.agent]
		case "enter":
			return m, f.move(1)
		}
		return m, nil
	case fieldPlan:
		switch msg.String() {
		case " ", "left", "right", "h", "l":
			f.claudePlan = !f.claudePlan
		case "enter":
			return m, f.move(1)
		}
		return m, nil
	}
	if msg.String() == "enter" {
		return m, m.startFromForm()
	}
	var cmd tea.Cmd
	f.prompt, cmd = f.prompt.Update(msg)
	return m, cmd
}

func (f *newRunForm) suggestions() []posthogcloud.Repository {
	return f.repositories[:min(len(f.repositories), maxSuggestions)]
}

func (f *newRunForm) choose() bool {
	suggestions := f.suggestions()
	typed := strings.ToLower(strings.TrimSpace(f.repository.Value()))
	for i := range f.repositories {
		if f.repositories[i].Name == typed {
			f.chosen = &f.repositories[i]
		}
	}
	if f.chosen == nil && f.pick < len(suggestions) {
		f.chosen = &suggestions[f.pick]
	}
	if f.chosen == nil {
		return false
	}
	f.repository.SetValue(f.chosen.Name)
	f.repository.CursorEnd()
	return true
}

func (m *manager) startFromForm() tea.Cmd {
	f := &m.form
	prompt := strings.TrimSpace(f.prompt.Value())
	switch {
	case f.chosen == nil:
		f.failure = "Pick a repository first."
		f.field = fieldRepository
		return f.focus()
	case prompt == "":
		f.failure = "Write a prompt first."
		return nil
	case f.planAvailable() && f.claudePlan && m.config.ClaudeToken == "":
		f.failure = "Save a Claude token first: esc, then T."
		return nil
	}
	f.starting, f.failure = true, ""
	request := posthogcloud.StartRequest{Repository: *f.chosen, Agent: f.agent, ClaudePlan: f.planAvailable() && f.claudePlan, Prompt: prompt}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*actionTimeout)
		defer cancel()
		task, run, err := client.StartRun(ctx, request)
		if err == nil {
			task = startedTask(task, run, request)
		}
		return startedMsg{task: task, err: err}
	}
}

func startedTask(task posthogcloud.Task, run posthogcloud.Run, request posthogcloud.StartRequest) posthogcloud.Task {
	task.Description = cmp.Or(task.Description, request.Prompt)
	task.Repository = cmp.Or(task.Repository, request.Repository.Name)
	task.LatestRun = &posthogcloud.RunSummary{ID: run.ID, Status: cmp.Or(run.Status, "queued")}
	return task
}

func (f *newRunForm) view(m *manager) string {
	width := contentWidth(m.width)
	f.styleInputs()
	var b strings.Builder
	b.WriteString(titleStyle.Render("New cloud run") + "\n\n")
	if f.field == fieldPrompt {
		b.WriteString(f.summary(m, width) + "\n\n")
		b.WriteString(f.prompt.View() + "\n")
	} else {
		b.WriteString(f.settings(m, width))
		b.WriteString("\n" + label("Prompt", false) + "\n" + f.prompt.View() + "\n")
	}
	keys := []keyHint{{"↵", "start"}, {"⌥↵", "new line"}, {"tab", "change"}, {"esc", "cancel"}}
	switch f.field {
	case fieldRepository:
		keys = []keyHint{{"↵", "pick"}, {"↑↓", "choose"}, {"tab", "next"}, {"esc", "cancel"}}
	case fieldAgent, fieldPlan:
		keys = []keyHint{{"←→", "change"}, {"↵", "next"}, {"esc", "cancel"}}
	}
	footer := hints(keys, width)
	switch {
	case f.starting:
		footer = warnStyle.Render("Starting the run…") + "\n\n" + footer
	case f.failure != "":
		footer = alertStyle.Render(wrap(f.failure, width)) + "\n\n" + footer
	}
	return frame(m.width, m.height, b.String(), footer)
}

func (f *newRunForm) summary(m *manager, width int) string {
	repository := warnStyle.Render("no repository")
	if f.chosen != nil {
		repository = f.chosen.Name
	}
	parts := []string{repository, map[string]string{posthogcloud.AgentClaude: "Claude", posthogcloud.AgentCodex: "Codex"}[f.agent]}
	if f.planAvailable() {
		billing := "PostHog pays"
		if f.claudePlan {
			billing = "my Claude plan"
			if m.config.ClaudeToken == "" {
				billing = warnStyle.Render("my Claude plan (no token)")
			}
		}
		parts = append(parts, billing)
	}
	return dimStyle.Render(wrap(strings.Join(parts, " · "), width))
}

func (f *newRunForm) settings(m *manager, width int) string {
	var b strings.Builder
	b.WriteString(label("Repository", f.field == fieldRepository) + "\n" + f.repository.View() + "\n")
	if f.field == fieldRepository && f.chosen == nil {
		switch {
		case f.loading && len(f.repositories) == 0:
			b.WriteString(dimStyle.Render("  Searching…") + "\n")
		case len(f.repositories) == 0:
			b.WriteString(dimStyle.Render("  No repository matches") + "\n")
		}
		for i, repository := range f.suggestions() {
			name := truncateWidth(repository.Name, width-2)
			if i == f.pick {
				b.WriteString(accentStyle.Render("▸ ") + boldStyle.Render(name) + "\n")
			} else {
				b.WriteString("  " + dimStyle.Render(name) + "\n")
			}
		}
	}
	agent := 0
	if f.agent == posthogcloud.AgentCodex {
		agent = 1
	}
	b.WriteString("\n" + label("Agent", f.field == fieldAgent) + "\n" + pills([]string{"Claude", "Codex"}, agent, f.field == fieldAgent, width) + "\n")
	if f.planAvailable() {
		plan := 0
		if f.claudePlan {
			plan = 1
		}
		b.WriteString("\n" + label("Claude billing", f.field == fieldPlan) + "\n" + pills([]string{"PostHog", "My plan"}, plan, f.field == fieldPlan, width) + "\n")
		if f.claudePlan && m.config.ClaudeToken == "" {
			b.WriteString(warnStyle.Render(wrap("No Claude token saved yet. Press esc, then T.", width)) + "\n")
		}
	}
	return b.String()
}

func (f *newRunForm) styleInputs() {
	bar := dimStyle
	if f.field == fieldRepository {
		bar = barStyle
	}
	f.repository.PromptStyle = bar
	f.repository.Prompt = "┃ "
	f.repository.PlaceholderStyle = dimStyle
	f.repository.TextStyle = lipgloss.NewStyle()
	f.prompt.Prompt = "┃ "
	f.prompt.FocusedStyle.Prompt = barStyle
	f.prompt.BlurredStyle.Prompt = dimStyle
	f.prompt.FocusedStyle.CursorLine = lipgloss.NewStyle()
	f.prompt.BlurredStyle.CursorLine = lipgloss.NewStyle()
	f.prompt.FocusedStyle.Placeholder = dimStyle
	f.prompt.BlurredStyle.Placeholder = dimStyle
	f.prompt.FocusedStyle.Base = lipgloss.NewStyle()
	f.prompt.BlurredStyle.Base = lipgloss.NewStyle()
}

func startRunFromCLI(paths paths, args []string) error {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	repository := flags.String("repo", "", "the GitHub repository to work in, as owner/name")
	agent := flags.String("agent", posthogcloud.AgentClaude, "claude or codex")
	claudePlan := flags.Bool("claude-plan", false, "bill Claude to your own plan (needs `phcloud claude-token`)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *repository == "" || prompt == "" {
		return errors.New(`usage: phcloud new --repo owner/name [--agent claude|codex] [--claude-plan] "what to do"`)
	}
	config, err := loadConfig(paths)
	if err != nil {
		return err
	}
	if *claudePlan && config.ClaudeToken == "" {
		return errors.New("save a Claude token first with `phcloud claude-token`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := posthogcloud.NewClient(config, nil)
	wanted := strings.ToLower(strings.TrimSpace(*repository))
	_, short, _ := strings.Cut(wanted, "/")
	found, err := client.Repositories(ctx, cmp.Or(short, wanted))
	if err != nil {
		return fmt.Errorf("list your repositories: %w", err)
	}
	var chosen *posthogcloud.Repository
	for i := range found {
		if found[i].Name == wanted {
			chosen = &found[i]
		}
	}
	if chosen == nil {
		return fmt.Errorf("PostHog can't reach %s through your GitHub integrations", wanted)
	}
	task, run, err := client.StartRun(ctx, posthogcloud.StartRequest{Repository: *chosen, Agent: *agent, ClaudePlan: *claudePlan, Prompt: prompt})
	if err != nil {
		return err
	}
	task = startedTask(task, run, posthogcloud.StartRequest{Repository: *chosen, Prompt: prompt})
	fmt.Printf("Started %s/project/%d/tasks/%s\n", strings.TrimSuffix(config.Host, "/"), config.ProjectID, task.ID)
	if err := newCmux(paths.workspaces).open(ctx, task); err != nil {
		fmt.Printf("Follow it with: phcloud attach %s %s\n", task.ID, run.ID)
	}
	return nil
}
