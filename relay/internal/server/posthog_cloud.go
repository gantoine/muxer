package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

const (
	postHogCloudFeature          = "posthog_cloud"
	postHogCloudActionsFeature   = "posthog_cloud_actions"
	postHogCloudActionTimeout    = 3 * time.Minute
	postHogCloudTaskLimit        = 50
	postHogCloudBackgroundPoll   = 30 * time.Second
	postHogCloudBackgroundRuns   = 8
	postHogCloudSubscriberBuffer = 256
)

var errPostHogCloudNotConnected = &requestError{Code: "posthog_cloud_not_connected", Message: "PostHog Cloud isn't connected on this computer. Run `muxer posthog-cloud connect` there."}

type postHogCloudTask struct {
	ID         string `json:"id"`
	Number     int    `json:"number,omitempty"`
	Title      string `json:"title"`
	Repository string `json:"repository,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	Status     string `json:"status"`
	State      string `json:"state,omitempty"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	URL        string `json:"url"`
}

type postHogCloudTasksResponse struct {
	Connected bool               `json:"connected"`
	Account   string             `json:"account,omitempty"`
	Tasks     []postHogCloudTask `json:"tasks"`
}

type postHogCloudRunView struct {
	TaskID      string `json:"task_id"`
	RunID       string `json:"run_id"`
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Status      string `json:"status"`
	State       string `json:"state,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PullRequest string `json:"pull_request,omitempty"`
	Model       string `json:"model,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) postHogCloudClient() (*posthogcloud.Client, posthogcloud.Config, bool, error) {
	if s.options.PostHogCloudConfig == "" {
		return nil, posthogcloud.Config{}, false, nil
	}
	config, ok, err := posthogcloud.LoadConfig(s.options.PostHogCloudConfig)
	if err != nil || !ok {
		return nil, config, false, err
	}
	return posthogcloud.NewClient(config, s.options.PostHogCloudHTTP), config, true, nil
}

func (s *Server) postHogCloudTasks(ctx context.Context) (postHogCloudTasksResponse, error) {
	client, config, ok, err := s.postHogCloudClient()
	if err != nil {
		return postHogCloudTasksResponse{}, err
	}
	if !ok {
		return postHogCloudTasksResponse{Tasks: []postHogCloudTask{}}, nil
	}
	tasks, err := client.Tasks(ctx, postHogCloudTaskLimit)
	if err != nil {
		return postHogCloudTasksResponse{}, postHogCloudError(err)
	}
	response := postHogCloudTasksResponse{Connected: true, Account: config.Email, Tasks: make([]postHogCloudTask, 0, len(tasks))}
	for _, task := range tasks {
		view := postHogCloudTask{
			ID:         task.ID,
			Number:     task.Number,
			Title:      strings.TrimSpace(task.Title),
			Repository: task.Repository,
			Status:     "not_started",
			UpdatedAt:  task.UpdatedAt,
			URL:        postHogCloudTaskURL(config, task.ID),
		}
		if run := task.LatestRun; run != nil {
			view.RunID = run.ID
			view.Status = run.Status
			view.Error = run.ErrorMessage
			view.UpdatedAt = latest(task.UpdatedAt, run.CreatedAt, run.CompletedAt)
			if status, state := s.cloud.live(run.ID); status != "" {
				view.Status = status
				if status == "in_progress" {
					view.State = state
				}
			}
		}
		response.Tasks = append(response.Tasks, view)
	}
	return response, nil
}

func postHogCloudTaskURL(config posthogcloud.Config, taskID string) string {
	return strings.TrimSuffix(config.Host, "/") + "/project/" + strconv.Itoa(config.ProjectID) + "/tasks/" + taskID
}

func postHogCloudError(err error) error {
	var streamErr *posthogcloud.StreamError
	switch {
	case errors.Is(err, posthogcloud.ErrUnauthorized):
		return &requestError{Code: "posthog_cloud_unauthorized", Message: "PostHog refused the API key. Run `muxer posthog-cloud connect` on the computer with a new one."}
	case errors.Is(err, posthogcloud.ErrForbidden):
		return &requestError{Code: "posthog_cloud_forbidden", Message: "The API key can't read Tasks. It needs the task:read scope, and your organization needs Tasks."}
	case errors.As(err, &streamErr):
		return &requestError{Code: "posthog_cloud_unavailable", Message: "PostHog's live stream for this run failed. Retrying."}
	}
	return &requestError{Code: "posthog_cloud_unavailable", Message: err.Error()}
}

func latest(times ...string) string {
	var newest string
	var newestTime time.Time
	for _, value := range times {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err == nil && parsed.After(newestTime) {
			newest, newestTime = value, parsed
		}
	}
	return newest
}

type cloudRunWatch struct {
	runID  string
	sub    *cloudSubscription
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *session) watchPostHogCloudRun(taskID, runID string) error {
	client, config, ok, err := s.server.postHogCloudClient()
	if err != nil {
		return err
	}
	if !ok {
		return errPostHogCloudNotConnected
	}
	taskURL := postHogCloudTaskURL(config, taskID)
	s.unwatchPostHogCloudRun()
	sub := s.server.cloud.subscribe(client, taskID, runID)
	ctx, cancel := context.WithCancel(s.ctx)
	watch := &cloudRunWatch{runID: runID, sub: sub, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.cloudRun = watch
	s.mu.Unlock()
	go func() {
		defer close(watch.done)
		s.forwardPostHogCloudRun(ctx, taskID, runID, taskURL, sub)
	}()
	return nil
}

func (s *session) unwatchPostHogCloudRun() {
	s.mu.Lock()
	current := s.cloudRun
	s.cloudRun = nil
	s.mu.Unlock()
	if current == nil {
		return
	}
	current.cancel()
	<-current.done
	s.server.cloud.unsubscribe(current.runID, current.sub)
}

func (s *session) forwardPostHogCloudRun(ctx context.Context, taskID, runID, taskURL string, sub *cloudSubscription) {
	var run posthogcloud.Run
	var lastView postHogCloudRunView
	for {
		var update posthogcloud.Update
		select {
		case <-ctx.Done():
			return
		case update = <-sub.updates:
		case <-sub.wake:
		}
		if sub.stale.Swap(false) {
			for len(sub.updates) > 0 {
				<-sub.updates
			}
			update = snapshotUpdate(sub.feed.current())
		}
		if update.Err != nil {
			s.send(map[string]any{"type": "posthog_cloud_run_error", "run_id": runID, "error": postHogCloudError(update.Err)})
			continue
		}
		switch {
		case update.Reset:
			s.sendPostHogCloudChat(runID, update.Entries, true)
		case len(update.Entries) > 0:
			s.sendPostHogCloudChat(runID, update.Entries, false)
		}
		if update.Run != nil {
			run = *update.Run
		}
		if run.Status == "" {
			continue
		}
		view := postHogCloudRunView{
			TaskID:      taskID,
			RunID:       runID,
			Title:       s.server.cloud.knownTitle(runID),
			URL:         taskURL,
			Status:      run.Status,
			Branch:      run.Branch,
			PullRequest: run.PullRequestURL(),
			Model:       run.Model,
			Agent:       run.RuntimeAdapter,
			Error:       run.ErrorMessage,
		}
		if run.Status == "in_progress" {
			view.State = update.State
			if view.State == "" {
				view.State = lastView.State
			}
		}
		if view != lastView {
			lastView = view
			s.send(map[string]any{"type": "posthog_cloud_run", "run": view})
		}
	}
}

func (s *session) sendPostHogCloudChat(runID string, entries []chat.Entry, reset bool) {
	if reset && len(entries) > maxInitialEntries {
		entries = entries[len(entries)-maxInitialEntries:]
	}
	if entries == nil {
		entries = []chat.Entry{}
	}
	s.send(map[string]any{
		"type":    "posthog_cloud_chat",
		"run_id":  runID,
		"reset":   reset,
		"entries": entries,
	})
}

func (s *session) mirroredRun(current *attachment) (runID, taskID string, ok bool) {
	if current == nil {
		return "", "", false
	}
	runID, mirrored, ok := s.server.mirror.runForSurface(current.surfaceID)
	return runID, mirrored.TaskID, ok
}

func (s *session) mirroredRequest(kind string, current *attachment) bool {
	switch kind {
	case "paste", "send_agent_message", "send_images":
		_, _, ok := s.mirroredRun(current)
		return ok
	}
	return false
}

func (s *session) actOnMirroredRun(kind string, raw json.RawMessage, current *attachment) (any, bool, error) {
	runID, taskID, ok := s.mirroredRun(current)
	if !ok {
		return nil, false, nil
	}
	var request struct {
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
		Key    string `json:"key"`
	}
	_ = json.Unmarshal(raw, &request)
	client, _, connected, err := s.server.postHogCloudClient()
	if err == nil && !connected {
		err = errPostHogCloudNotConnected
	}
	ctx, cancel := context.WithTimeout(s.ctx, postHogCloudActionTimeout)
	defer cancel()
	switch kind {
	case "key":
		if request.Key != "escape" && request.Key != "ctrl-c" {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		return nil, true, s.server.cloud.stopTurn(ctx, client, taskID, runID)
	case "paste":
		if !request.Submit {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		return nil, true, s.sendToMirroredRun(ctx, client, taskID, runID, request.Text)
	case "send_agent_message":
		if err != nil {
			return nil, true, err
		}
		if err := s.sendToMirroredRun(ctx, client, taskID, runID, request.Text); err != nil {
			return nil, true, err
		}
		now := time.Now()
		return cmux.AgentMessage{ID: "phc-" + strconv.FormatInt(now.UnixNano(), 36), Body: strings.TrimSpace(request.Text), State: "delivered", CreatedAt: float64(now.UnixMilli()) / 1000}, true, nil
	case "send_images":
		if err != nil {
			return nil, true, err
		}
		var images struct {
			Text   string           `json:"text"`
			Images []cmux.ChatImage `json:"images"`
		}
		if json.Unmarshal(raw, &images) != nil {
			return nil, true, invalid("send_images needs images and optional text.")
		}
		if err := validImages(images.Images); err != nil {
			return nil, true, err
		}
		uploads := make([]posthogcloud.Upload, len(images.Images))
		for i, image := range images.Images {
			data, _ := base64.StdEncoding.DecodeString(image.Data)
			format := strings.ToLower(image.Format)
			if format == "jpg" {
				format = "jpeg"
			}
			uploads[i] = posthogcloud.Upload{Name: fmt.Sprintf("image-%d.%s", i+1, format), ContentType: "image/" + format, Data: data}
		}
		return nil, true, s.server.cloud.sendMessage(ctx, client, taskID, runID, images.Text, uploads...)
	}
	return nil, false, nil
}

func (s *Server) postHogCloudImage(ctx context.Context, ref string) (cmux.ChatImage, error) {
	client, _, ok, err := s.postHogCloudClient()
	if err != nil {
		return cmux.ChatImage{}, err
	}
	if !ok {
		return cmux.ChatImage{}, errPostHogCloudNotConnected
	}
	return s.cloud.image(ctx, client, ref)
}

func (s *session) sendToMirroredRun(ctx context.Context, client *posthogcloud.Client, taskID, runID, text string) error {
	if err := validText("message", text); err != nil || strings.TrimSpace(text) == "" {
		return invalid("Write a message to send.")
	}
	return s.server.cloud.sendMessage(ctx, client, taskID, runID, strings.TrimSpace(text))
}

func (s *session) followMirroredChat(ctx context.Context, current *attachment, taskID, runID string) {
	client, _, ok, err := s.server.postHogCloudClient()
	if err != nil || !ok {
		s.sendChatSession(current, "")
		return
	}
	sub := s.server.cloud.subscribe(client, taskID, runID)
	defer s.server.cloud.unsubscribe(runID, sub)
	s.sendChatSession(current, runID)
	for {
		var update posthogcloud.Update
		select {
		case <-ctx.Done():
			return
		case update = <-sub.updates:
		case <-sub.wake:
		}
		if sub.stale.Swap(false) {
			for len(sub.updates) > 0 {
				<-sub.updates
			}
			update = snapshotUpdate(sub.feed.current())
		}
		switch {
		case update.Reset:
			entries := update.Entries
			if len(entries) > maxInitialEntries {
				entries = entries[len(entries)-maxInitialEntries:]
			}
			s.sendChat(current, runID, entries, true)
		case len(update.Entries) > 0:
			s.sendChat(current, runID, update.Entries, false)
		}
	}
}

const (
	postHogCloudStartFeature = "posthog_cloud_start"
	postHogCloudMirrorWait   = 500 * time.Millisecond
)

type postHogCloudOptions struct {
	Connected  bool     `json:"connected"`
	Agents     []string `json:"agents"`
	ClaudePlan bool     `json:"claude_plan"`
}

func (s *Server) postHogCloudOptions() (postHogCloudOptions, error) {
	_, config, ok, err := s.postHogCloudClient()
	if err != nil || !ok {
		return postHogCloudOptions{Agents: []string{}}, err
	}
	return postHogCloudOptions{
		Connected:  true,
		Agents:     []string{posthogcloud.AgentClaude, posthogcloud.AgentCodex},
		ClaudePlan: config.ClaudeToken != "",
	}, nil
}

func (s *Server) postHogCloudRepositories(ctx context.Context, query string) (map[string]any, error) {
	client, _, ok, err := s.postHogCloudClient()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errPostHogCloudNotConnected
	}
	repositories, err := client.Repositories(ctx, query)
	if err != nil {
		return nil, postHogCloudError(err)
	}
	if repositories == nil {
		repositories = []posthogcloud.Repository{}
	}
	return map[string]any{"repositories": repositories}, nil
}

func (s *Server) startPostHogCloudRun(ctx context.Context, request posthogcloud.StartRequest) (terminalRef, error) {
	client, config, ok, err := s.postHogCloudClient()
	if err != nil {
		return terminalRef{}, err
	}
	if !ok {
		return terminalRef{}, errPostHogCloudNotConnected
	}
	if request.ClaudePlan && config.ClaudeToken == "" {
		return terminalRef{}, &requestError{Code: "posthog_cloud_no_claude_token", Message: "No Claude token is saved on the computer. Run `muxer posthog-cloud claude-token` there first."}
	}
	_, run, err := client.StartRun(ctx, request)
	if err != nil {
		return terminalRef{}, postHogCloudError(err)
	}
	for {
		s.SyncPostHogCloud(ctx)
		if mirrored, ok := s.mirror.run(run.ID); ok {
			return terminalRef{WorkspaceID: mirrored.WorkspaceID, SurfaceID: mirrored.SurfaceID}, nil
		}
		select {
		case <-ctx.Done():
			return terminalRef{}, &requestError{Code: "posthog_cloud_started", Message: "The run started, but its workspace didn't open in time. It will appear in the workspace list shortly."}
		case <-time.After(postHogCloudMirrorWait):
		}
	}
}
