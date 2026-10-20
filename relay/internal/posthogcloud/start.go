package posthogcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	AgentClaude      = "claude"
	AgentCodex       = "codex"
	repositoryLimit  = 30
	teamSourcePrefix = "team:"
	userSourcePrefix = "user:"
)

var defaultModels = map[string]string{AgentClaude: "claude-sonnet-5-5", AgentCodex: "gpt-6.1-sol"}

type Repository struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
}

type integration struct {
	ID             json.RawMessage `json:"id"`
	Kind           string          `json:"kind"`
	InstallationID json.RawMessage `json:"installation_id"`
}

func (i integration) id() string {
	return rawString(i.ID)
}

func rawString(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func decodeIntegrations(raw []byte) []integration {
	var page struct {
		Results []integration `json:"results"`
	}
	if json.Unmarshal(raw, &page) == nil && page.Results != nil {
		return page.Results
	}
	var list []integration
	_ = json.Unmarshal(raw, &list)
	return list
}

func (c *Client) getRaw(ctx context.Context, path string, query url.Values) ([]byte, error) {
	var raw json.RawMessage
	err := c.getJSON(ctx, path, query, &raw)
	return raw, err
}

func (c *Client) Repositories(ctx context.Context, search string) ([]Repository, error) {
	query := url.Values{"search": {strings.TrimSpace(search)}, "limit": {strconv.Itoa(repositoryLimit)}, "offset": {"0"}, "compact": {"true"}}
	type source struct {
		prefix, id, path string
	}
	var sources []source
	var failures []error
	environment := "/api/environments/" + strconv.Itoa(c.config.ProjectID) + "/integrations/"
	if raw, err := c.getRaw(ctx, environment, nil); err == nil {
		for _, found := range decodeIntegrations(raw) {
			if found.Kind == "github" && found.id() != "" {
				sources = append(sources, source{teamSourcePrefix, found.id(), environment + url.PathEscape(found.id()) + "/github_repos/"})
			}
		}
	} else {
		failures = append(failures, err)
	}
	if raw, err := c.getRaw(ctx, "/api/users/@me/integrations/", url.Values{"kind": {"github"}}); err == nil {
		for _, found := range decodeIntegrations(raw) {
			installation := rawString(found.InstallationID)
			if found.id() != "" && installation != "" && (found.Kind == "" || found.Kind == "github") {
				sources = append(sources, source{userSourcePrefix, found.id(), "/api/users/@me/integrations/github/" + url.PathEscape(installation) + "/repos/"})
			}
		}
	} else {
		failures = append(failures, err)
	}
	if len(sources) == 0 && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	seen := map[string]bool{}
	var repositories []Repository
	for _, from := range sources {
		var page struct {
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		if err := c.getJSON(ctx, from.path, query, &page); err != nil {
			failures = append(failures, err)
			continue
		}
		for _, repository := range page.Repositories {
			name := strings.ToLower(strings.TrimSpace(repository.FullName))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			repositories = append(repositories, Repository{Name: name, Source: from.prefix + from.id})
		}
	}
	if repositories == nil && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return repositories, nil
}

type StartRequest struct {
	Repository Repository
	Agent      string
	ClaudePlan bool
	Prompt     string
}

type ModelChoice struct {
	RuntimeAdapter string `json:"runtime_adapter"`
	Model          string `json:"model"`
}

func (c *Client) Models(ctx context.Context) ([]ModelChoice, error) {
	var page struct {
		Models []ModelChoice `json:"models"`
	}
	err := c.getJSON(ctx, c.projectPath("tasks/models/"), nil, &page)
	return page.Models, err
}

func (c *Client) model(ctx context.Context, agent string) string {
	models, err := c.Models(ctx)
	if err != nil || len(models) == 0 {
		return defaultModels[agent]
	}
	first := ""
	for _, choice := range models {
		if choice.RuntimeAdapter != agent {
			continue
		}
		if choice.Model == defaultModels[agent] {
			return choice.Model
		}
		if first == "" {
			first = choice.Model
		}
	}
	if first != "" {
		return first
	}
	return defaultModels[agent]
}

func (c *Client) StartRun(ctx context.Context, request StartRequest) (Task, Run, error) {
	if request.Agent != AgentClaude && request.Agent != AgentCodex {
		return Task{}, Run{}, fmt.Errorf("PostHog Cloud runs here use Claude or Codex, not %q", request.Agent)
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return Task{}, Run{}, errors.New("a cloud run needs a prompt")
	}
	task := map[string]any{
		"description":     prompt,
		"repository":      request.Repository.Name,
		"runtime":         "acp",
		"runtime_adapter": request.Agent,
		"origin_product":  "user_created",
	}
	switch source := request.Repository.Source; {
	case strings.HasPrefix(source, teamSourcePrefix):
		if id, err := strconv.Atoi(strings.TrimPrefix(source, teamSourcePrefix)); err == nil {
			task["github_integration"] = id
		}
	case strings.HasPrefix(source, userSourcePrefix):
		task["github_user_integration"] = strings.TrimPrefix(source, userSourcePrefix)
	}
	var created Task
	if err := c.postJSON(ctx, c.projectPath("tasks/"), task, &created); err != nil {
		return Task{}, Run{}, fmt.Errorf("create the task: %w", err)
	}
	run := map[string]any{
		"environment":        "cloud",
		"mode":               "interactive",
		"run_source":         "manual",
		"pr_authorship_mode": "user",
		"runtime_adapter":    request.Agent,
		"model":              c.model(ctx, request.Agent),
	}
	if request.Agent == AgentClaude {
		run["initial_permission_mode"] = "auto"
		if request.ClaudePlan {
			run["claude_model_access"] = "own-subscription"
		}
	}
	var started Run
	if err := c.postJSON(ctx, c.projectPath("tasks/"+url.PathEscape(created.ID)+"/runs/"), run, &started); err != nil {
		return created, Run{}, fmt.Errorf("create the run: %w", err)
	}
	if err := c.postJSON(ctx, c.runPath(created.ID, started.ID, "start/"), map[string]any{"pending_user_message": prompt}, nil); err != nil {
		return created, started, fmt.Errorf("start the run: %w", err)
	}
	return created, started, nil
}
