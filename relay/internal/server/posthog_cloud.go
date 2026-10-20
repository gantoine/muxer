package server

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

const (
	postHogCloudFeature      = "posthog_cloud"
	postHogCloudTaskLimit    = 50
	postHogCloudPollInterval = 2 * time.Second
	postHogCloudMaxBackoff   = 30 * time.Second
)

var errPostHogCloudNotConnected = &requestError{Code: "posthog_cloud_not_connected", Message: "PostHog Cloud isn't connected on this computer. Run `muxer posthog-cloud connect` there."}

type postHogCloudTask struct {
	ID         string `json:"id"`
	Number     int    `json:"number,omitempty"`
	Title      string `json:"title"`
	Repository string `json:"repository,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	Status     string `json:"status"`
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
			URL:        strings.TrimSuffix(config.Host, "/") + "/project/" + strconv.Itoa(config.ProjectID) + "/tasks/" + task.ID,
		}
		if run := task.LatestRun; run != nil {
			view.RunID = run.ID
			view.Status = run.Status
			view.Error = run.ErrorMessage
			view.UpdatedAt = latest(task.UpdatedAt, run.CreatedAt, run.CompletedAt)
		}
		response.Tasks = append(response.Tasks, view)
	}
	return response, nil
}

func postHogCloudError(err error) error {
	switch {
	case errors.Is(err, posthogcloud.ErrUnauthorized):
		return &requestError{Code: "posthog_cloud_unauthorized", Message: "PostHog refused the API key. Run `muxer posthog-cloud connect` on the computer with a new one."}
	case errors.Is(err, posthogcloud.ErrForbidden):
		return &requestError{Code: "posthog_cloud_forbidden", Message: "The API key can't read Tasks. It needs the task:read scope, and your organization needs Tasks."}
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

func (s *session) watchPostHogCloudRun(taskID, runID string) error {
	client, _, ok, err := s.server.postHogCloudClient()
	if err != nil {
		return err
	}
	if !ok {
		return errPostHogCloudNotConnected
	}
	s.unwatchPostHogCloudRun()
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.mu.Lock()
	s.cloudRun = &cloudRunWatch{cancel: cancel, done: done}
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.followPostHogCloudRun(ctx, client, taskID, runID)
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
}

type cloudRunWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type cloudRunFollower struct {
	session    *session
	client     *posthogcloud.Client
	taskID     string
	runID      string
	transcript *posthogcloud.Transcript
	offset     int
	sent       bool
	lastView   postHogCloudRunView
}

func (s *session) followPostHogCloudRun(ctx context.Context, client *posthogcloud.Client, taskID, runID string) {
	follower := &cloudRunFollower{session: s, client: client, taskID: taskID, runID: runID, transcript: posthogcloud.NewTranscript()}
	interval := s.server.options.PollInterval.PostHogCloud
	if interval <= 0 {
		interval = postHogCloudPollInterval
	}
	wait := interval
	for {
		finished, err := follower.poll(ctx)
		if ctx.Err() != nil || finished {
			return
		}
		if err != nil {
			s.server.logger.Warn("could not follow the PostHog Cloud run", "run", runID, "error", err)
			s.send(map[string]any{"type": "posthog_cloud_run_error", "run_id": runID, "error": postHogCloudError(err)})
			wait = min(wait*2, postHogCloudMaxBackoff)
		} else {
			wait = interval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (f *cloudRunFollower) poll(ctx context.Context) (bool, error) {
	run, err := f.client.Run(ctx, f.taskID, f.runID)
	if err != nil {
		return false, err
	}
	if run.Terminal() && f.sent {
		f.transcript = posthogcloud.NewTranscript()
		f.offset = 0
		f.sent = false
	}
	changed, err := f.readLog(ctx)
	if err != nil {
		return false, err
	}
	if !f.sent {
		f.sent = true
		f.sendChat(f.transcript.Entries(), true)
	} else if len(changed) > 0 {
		f.sendChat(changed, false)
	}
	f.sendRun(run)
	return run.Terminal(), nil
}

func (f *cloudRunFollower) readLog(ctx context.Context) ([]chat.Entry, error) {
	var changed []chat.Entry
	for {
		entries, more, err := f.client.SessionLogs(ctx, f.taskID, f.runID, f.offset)
		if err != nil {
			return nil, err
		}
		for _, raw := range entries {
			changed = append(changed, f.transcript.Feed(raw)...)
		}
		f.offset += len(entries)
		if !more || len(entries) == 0 {
			return changed, nil
		}
	}
}

func (f *cloudRunFollower) sendChat(entries []chat.Entry, reset bool) {
	if reset && len(entries) > maxInitialEntries {
		entries = entries[len(entries)-maxInitialEntries:]
	}
	if entries == nil {
		entries = []chat.Entry{}
	}
	f.session.send(map[string]any{
		"type":    "posthog_cloud_chat",
		"run_id":  f.runID,
		"reset":   reset,
		"entries": entries,
	})
}

func (f *cloudRunFollower) sendRun(run posthogcloud.Run) {
	view := postHogCloudRunView{
		TaskID:      f.taskID,
		RunID:       f.runID,
		Status:      run.Status,
		Branch:      run.Branch,
		PullRequest: run.PullRequestURL(),
		Model:       run.Model,
		Agent:       run.RuntimeAdapter,
		Error:       run.ErrorMessage,
	}
	if run.Status == "in_progress" {
		view.State = f.transcript.State()
	}
	if view == f.lastView {
		return
	}
	f.lastView = view
	f.session.send(map[string]any{"type": "posthog_cloud_run", "run": view})
}
