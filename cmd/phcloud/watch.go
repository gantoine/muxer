package main

import (
	"context"
	"sync"
	"time"

	"github.com/gantoine/phcloud/internal/posthogcloud"
)

const (
	maxWatchedRuns   = 12
	credentialWindow = 2 * time.Minute
	claudeCredential = "claude_subscription_token"
)

type watchedRun struct {
	task     posthogcloud.Task
	follower *posthogcloud.Follower
	cancel   context.CancelFunc
	answered map[string]bool
	seen     bool
	lastKey  string
}

type watcher struct {
	ctx     context.Context
	client  *posthogcloud.Client
	config  posthogcloud.Config
	cmux    *cmux
	changed func()

	mu   sync.Mutex
	runs map[string]*watchedRun
}

func newWatcher(ctx context.Context, client *posthogcloud.Client, config posthogcloud.Config, cmux *cmux, changed func()) *watcher {
	return &watcher{ctx: ctx, client: client, config: config, cmux: cmux, changed: changed, runs: map[string]*watchedRun{}}
}

func (w *watcher) sync(tasks []posthogcloud.Task) {
	w.mu.Lock()
	defer w.mu.Unlock()
	active := map[string]bool{}
	for _, task := range tasks {
		if task.LatestRun == nil || posthogcloud.Terminal(task.LatestRun.Status) || len(active) >= maxWatchedRuns {
			continue
		}
		runID := task.LatestRun.ID
		active[runID] = true
		if watched, ok := w.runs[runID]; ok && watched.follower != nil {
			watched.task = task
			continue
		}
		w.start(task)
	}
	for runID, watched := range w.runs {
		if !active[runID] && watched.follower == nil {
			delete(w.runs, runID)
		}
	}
}

func (w *watcher) start(task posthogcloud.Task) {
	runID := task.LatestRun.ID
	ctx, cancel := context.WithCancel(w.ctx)
	watched := &watchedRun{task: task, cancel: cancel, answered: map[string]bool{}}
	watched.follower = posthogcloud.NewFollower(w.client, task.ID, runID, posthogcloud.FollowerOptions{}, func(update posthogcloud.Update) {
		if update.Credential != nil {
			go w.answerCredential(task.ID, runID, *update.Credential)
			return
		}
		if update.Err == nil {
			w.observe(runID)
		}
	})
	w.runs[runID] = watched
	go func() {
		_ = watched.follower.Run(ctx)
		w.observe(runID)
		w.mu.Lock()
		watched.follower = nil
		w.mu.Unlock()
		cancel()
	}()
}

func (w *watcher) stopAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, watched := range w.runs {
		watched.cancel()
	}
}

type liveState struct {
	status  string
	state   string
	pending []posthogcloud.PendingApproval
}

func (w *watcher) live(runID string) (liveState, bool) {
	w.mu.Lock()
	watched, ok := w.runs[runID]
	var follower *posthogcloud.Follower
	if ok {
		follower = watched.follower
	}
	w.mu.Unlock()
	if follower == nil {
		return liveState{}, false
	}
	_, run, state, ready := follower.Snapshot()
	if !ready {
		return liveState{}, false
	}
	return liveState{status: run.Status, state: state, pending: follower.Pending()}, true
}

func (w *watcher) follower(runID string) *posthogcloud.Follower {
	w.mu.Lock()
	defer w.mu.Unlock()
	if watched, ok := w.runs[runID]; ok {
		return watched.follower
	}
	return nil
}

func (w *watcher) observe(runID string) {
	live, ok := w.live(runID)
	if !ok {
		w.changed()
		return
	}
	w.mu.Lock()
	watched := w.runs[runID]
	if watched == nil {
		w.mu.Unlock()
		return
	}
	key := live.status + "/" + live.state + "/" + firstRequestID(live.pending)
	first, previous := !watched.seen, watched.lastKey
	watched.seen, watched.lastKey = true, key
	task := watched.task
	w.mu.Unlock()
	w.cmux.setLane(w.ctx, runID, laneFor(live.status, live.state))
	if !first && key != previous {
		if title, body := notification(task, live); title != "" {
			w.cmux.notify(w.ctx, runID, title, body)
		}
	}
	w.changed()
}

func notification(task posthogcloud.Task, live liveState) (string, string) {
	title := taskTitle(task)
	switch live.status {
	case "completed":
		return "Cloud run finished", title
	case "failed":
		return "Cloud run failed", title
	}
	if live.state == posthogcloud.StateWaiting && len(live.pending) > 0 {
		return "Cloud run needs you", title + ": " + approvalSummary(live.pending[0])
	}
	if live.state == posthogcloud.StateIdle {
		return "Cloud run is waiting for you", title
	}
	return "", ""
}

func firstRequestID(pending []posthogcloud.PendingApproval) string {
	if len(pending) == 0 {
		return ""
	}
	return pending[0].RequestID
}

func (w *watcher) answerCredential(taskID, runID string, request posthogcloud.CredentialRequest) {
	if request.Credential != "" && request.Credential != claudeCredential {
		return
	}
	w.mu.Lock()
	watched := w.runs[runID]
	if watched == nil || watched.answered[request.RequestID] {
		w.mu.Unlock()
		return
	}
	watched.answered[request.RequestID] = true
	w.mu.Unlock()
	if w.config.ClaudeToken == "" {
		return
	}
	deadline := request.ExpiresAt
	if deadline.IsZero() || time.Until(deadline) > credentialWindow {
		deadline = time.Now().Add(credentialWindow)
	}
	ctx, cancel := context.WithDeadline(w.ctx, deadline)
	defer cancel()
	run, err := w.client.Run(ctx, taskID, runID)
	if err != nil || run.ClaudeSubscriptionUser() != w.config.UserID {
		return
	}
	params := map[string]any{"requestId": request.RequestID, "credential": claudeCredential, "token": w.config.ClaudeToken}
	_, _ = w.client.CommandWithRetry(ctx, taskID, runID, "credential_response", params)
}
