package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gantoine/phc/internal/posthogcloud"
)

const (
	repositoryDebounce = 300 * time.Millisecond
	maxSuggestions     = 6
)

const (
	fieldRepository = iota
	fieldAgent
	fieldPlan
	fieldPrompt
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

func (f *newRunForm) open(m *manager) tea.Cmd {
	*f = newRunForm{agent: posthogcloud.AgentClaude, claudePlan: m.config.ClaudeToken != ""}
	f.repository = textinput.New()
	f.repository.Placeholder = "owner/name"
	f.repository.Prompt = ""
	f.prompt = textarea.New()
	f.prompt.Placeholder = "What should the agent do?"
	f.prompt.ShowLineNumbers = false
	f.prompt.SetHeight(5)
	f.prompt.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	f.resize(m.width)
	f.loading = true
	return tea.Batch(f.repository.Focus(), m.findRepositories(""))
}

func (f *newRunForm) resize(width int) {
	f.repository.Width = max(10, width-4)
	f.prompt.SetWidth(max(10, width-2))
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
		return nil
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
	var b strings.Builder
	b.WriteString(boldStyle.Render("New cloud run") + "\n\n")
	label := func(field int, text string) string {
		if f.field == field {
			return accentStyle.Render("› " + text)
		}
		return dimStyle.Render("  " + text)
	}
	b.WriteString(label(fieldRepository, "Repository") + "\n  " + f.repository.View() + "\n")
	if f.field == fieldRepository && f.chosen == nil {
		switch {
		case f.loading:
			b.WriteString(dimStyle.Render("  searching…") + "\n")
		case len(f.repositories) == 0:
			b.WriteString(dimStyle.Render("  no repositories match") + "\n")
		}
		for i, repository := range f.suggestions() {
			if i == f.pick {
				b.WriteString("  " + selectedStyle.Render(repository.Name) + "\n")
			} else {
				b.WriteString("  " + repository.Name + "\n")
			}
		}
	}
	b.WriteString("\n" + label(fieldAgent, "Agent") + "  " + choice(f.agent == posthogcloud.AgentClaude, "Claude") + " " + choice(f.agent == posthogcloud.AgentCodex, "Codex") + "\n")
	if f.planAvailable() {
		plan := choice(!f.claudePlan, "PostHog pays") + " " + choice(f.claudePlan, "My Claude plan")
		if m.config.ClaudeToken == "" && f.claudePlan {
			plan += " " + warnStyle.Render("(no token saved)")
		}
		b.WriteString(label(fieldPlan, "Billing") + "  " + plan + "\n")
	}
	b.WriteString("\n" + label(fieldPrompt, "Prompt") + "\n" + f.prompt.View() + "\n\n")
	switch {
	case f.starting:
		b.WriteString(warnStyle.Render("Starting…") + "\n")
	case f.failure != "":
		b.WriteString(alertStyle.Render(f.failure) + "\n")
	}
	b.WriteString(dimStyle.Render(wrapKeys([]string{"tab next", "enter start", "alt+enter newline", "esc cancel"}, max(20, m.width))))
	return b.String()
}

func choice(on bool, text string) string {
	if on {
		return accentStyle.Render("(•) " + text)
	}
	return dimStyle.Render("( ) " + text)
}

func startRunFromCLI(paths paths, args []string) error {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	repository := flags.String("repo", "", "the GitHub repository to work in, as owner/name")
	agent := flags.String("agent", posthogcloud.AgentClaude, "claude or codex")
	claudePlan := flags.Bool("claude-plan", false, "bill Claude to your own plan (needs `phc claude-token`)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *repository == "" || prompt == "" {
		return errors.New(`usage: phc new --repo owner/name [--agent claude|codex] [--claude-plan] "what to do"`)
	}
	config, err := loadConfig(paths)
	if err != nil {
		return err
	}
	if *claudePlan && config.ClaudeToken == "" {
		return errors.New("save a Claude token first with `phc claude-token`")
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
		fmt.Printf("Follow it with: phc attach %s %s\n", task.ID, run.ID)
	}
	return nil
}
