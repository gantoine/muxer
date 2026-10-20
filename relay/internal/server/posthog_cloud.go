package server

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

const (
	postHogCloudFeature          = "posthog_cloud"
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

type cloudHub struct {
	server *Server
	mu     sync.Mutex
	feeds  map[string]*cloudFeed
}

type cloudFeed struct {
	taskID     string
	follower   *posthogcloud.Follower
	cancel     context.CancelFunc
	mu         sync.Mutex
	subs       map[*cloudSubscription]bool
	refs       int
	background bool
}

type cloudSubscription struct {
	feed    *cloudFeed
	updates chan posthogcloud.Update
	wake    chan struct{}
	stale   atomic.Bool
}

func newCloudHub(server *Server) *cloudHub {
	return &cloudHub{server: server, feeds: map[string]*cloudFeed{}}
}

func (h *cloudHub) acquire(client *posthogcloud.Client, taskID, runID string) *cloudFeed {
	h.mu.Lock()
	defer h.mu.Unlock()
	if feed, ok := h.feeds[runID]; ok {
		feed.refs++
		return feed
	}
	ctx, cancel := context.WithCancel(h.server.ctx)
	feed := &cloudFeed{taskID: taskID, cancel: cancel, subs: map[*cloudSubscription]bool{}, refs: 1}
	feed.follower = posthogcloud.NewFollower(client, taskID, runID, h.server.options.PostHogCloudFollower, feed.dispatch)
	h.feeds[runID] = feed
	go func() {
		if err := feed.follower.Run(ctx); err != nil && ctx.Err() == nil {
			h.server.logger.Warn("stopped following a PostHog Cloud run", "run", runID, "error", err)
		}
	}()
	return feed
}

func (h *cloudHub) release(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	feed, ok := h.feeds[runID]
	if !ok {
		return
	}
	feed.refs--
	if feed.refs <= 0 {
		feed.cancel()
		delete(h.feeds, runID)
	}
}

func (h *cloudHub) live(runID string) (status, state string) {
	h.mu.Lock()
	feed, ok := h.feeds[runID]
	h.mu.Unlock()
	if !ok {
		return "", ""
	}
	_, run, state, ready := feed.follower.Snapshot()
	if !ready {
		return run.Status, ""
	}
	return run.Status, state
}

func (h *cloudHub) subscribe(client *posthogcloud.Client, taskID, runID string) *cloudSubscription {
	feed := h.acquire(client, taskID, runID)
	sub := &cloudSubscription{feed: feed, updates: make(chan posthogcloud.Update, postHogCloudSubscriberBuffer), wake: make(chan struct{}, 1)}
	feed.mu.Lock()
	sub.offer(snapshotUpdate(feed.follower))
	feed.subs[sub] = true
	feed.mu.Unlock()
	return sub
}

func (h *cloudHub) unsubscribe(runID string, sub *cloudSubscription) {
	sub.feed.mu.Lock()
	delete(sub.feed.subs, sub)
	sub.feed.mu.Unlock()
	h.release(runID)
}

func (h *cloudHub) setBackground(client *posthogcloud.Client, runs map[string]string) {
	h.mu.Lock()
	var dropped []string
	for runID, feed := range h.feeds {
		if feed.background && runs[runID] == "" {
			feed.background = false
			dropped = append(dropped, runID)
		}
	}
	var added []string
	for runID := range runs {
		if feed, ok := h.feeds[runID]; !ok || !feed.background {
			added = append(added, runID)
		}
	}
	h.mu.Unlock()
	for _, runID := range dropped {
		h.release(runID)
	}
	for _, runID := range added {
		feed := h.acquire(client, runs[runID], runID)
		h.mu.Lock()
		if feed.background {
			feed.refs--
		}
		feed.background = true
		h.mu.Unlock()
	}
}

func (h *cloudHub) run(ctx context.Context) {
	interval := h.server.options.PollInterval.PostHogCloud
	if interval <= 0 {
		interval = postHogCloudBackgroundPoll
	}
	for {
		h.refreshBackground(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (h *cloudHub) refreshBackground(ctx context.Context) {
	client, _, ok, err := h.server.postHogCloudClient()
	if err != nil || !ok {
		h.setBackground(nil, nil)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	tasks, err := client.Tasks(callCtx, postHogCloudTaskLimit)
	if err != nil {
		if ctx.Err() == nil {
			h.server.logger.Warn("could not list PostHog Cloud tasks", "error", err)
		}
		return
	}
	runs := map[string]string{}
	for _, task := range tasks {
		if run := task.LatestRun; run != nil && !posthogcloud.Terminal(run.Status) && run.Status != "not_started" && len(runs) < postHogCloudBackgroundRuns {
			runs[run.ID] = task.ID
		}
	}
	h.setBackground(client, runs)
}

func (f *cloudFeed) dispatch(update posthogcloud.Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for sub := range f.subs {
		sub.offer(update)
	}
}

func (s *cloudSubscription) offer(update posthogcloud.Update) {
	select {
	case s.updates <- update:
	default:
		s.stale.Store(true)
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func snapshotUpdate(follower *posthogcloud.Follower) posthogcloud.Update {
	entries, run, state, ready := follower.Snapshot()
	if !ready {
		if run.ID == "" {
			return posthogcloud.Update{}
		}
		return posthogcloud.Update{Run: &run, State: state}
	}
	return posthogcloud.Update{Reset: true, Entries: entries, Run: &run, State: state}
}

type cloudRunWatch struct {
	runID  string
	sub    *cloudSubscription
	cancel context.CancelFunc
	done   chan struct{}
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
	sub := s.server.cloud.subscribe(client, taskID, runID)
	ctx, cancel := context.WithCancel(s.ctx)
	watch := &cloudRunWatch{runID: runID, sub: sub, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.cloudRun = watch
	s.mu.Unlock()
	go func() {
		defer close(watch.done)
		s.forwardPostHogCloudRun(ctx, taskID, runID, sub)
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

func (s *session) forwardPostHogCloudRun(ctx context.Context, taskID, runID string, sub *cloudSubscription) {
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
			update = snapshotUpdate(sub.feed.follower)
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
