package server

import (
	"cmp"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gantoine/muxer/relay/internal/approvals"
	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/notify"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
	"github.com/gantoine/muxer/relay/internal/pubsub"
)

const (
	postHogCloudApprovalPrefix   = "phc:"
	postHogCloudCredentialWindow = 120 * time.Second
)

type cloudHub struct {
	server  *Server
	changes pubsub.Broadcaster

	mu     sync.Mutex
	feeds  map[string]*cloudFeed
	titles map[string]string
}

type cloudFeed struct {
	hub    *cloudHub
	client *posthogcloud.Client
	taskID string
	runID  string
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	follower   *posthogcloud.Follower
	running    bool
	subs       map[*cloudSubscription]bool
	refs       int
	background bool
	baseline   bool
	lastState  string
	lastStatus string
	answered   map[string]bool
}

type cloudSubscription struct {
	feed    *cloudFeed
	updates chan posthogcloud.Update
	wake    chan struct{}
	stale   atomic.Bool
}

func newCloudHub(server *Server) *cloudHub {
	return &cloudHub{server: server, feeds: map[string]*cloudFeed{}, titles: map[string]string{}}
}

func (h *cloudHub) acquire(client *posthogcloud.Client, taskID, runID string) *cloudFeed {
	h.mu.Lock()
	defer h.mu.Unlock()
	if feed, ok := h.feeds[runID]; ok {
		feed.refs++
		return feed
	}
	ctx, cancel := context.WithCancel(h.server.ctx)
	feed := &cloudFeed{hub: h, client: client, taskID: taskID, runID: runID, ctx: ctx, cancel: cancel, subs: map[*cloudSubscription]bool{}, refs: 1, answered: map[string]bool{}}
	h.feeds[runID] = feed
	feed.start()
	return feed
}

func (h *cloudHub) release(runID string) {
	h.mu.Lock()
	feed, ok := h.feeds[runID]
	if ok {
		feed.refs--
		if feed.refs > 0 {
			ok = false
		} else {
			delete(h.feeds, runID)
		}
	}
	h.mu.Unlock()
	if ok {
		feed.cancel()
		h.changes.Notify()
	}
}

func (h *cloudHub) feed(runID string) (*cloudFeed, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	feed, ok := h.feeds[runID]
	return feed, ok
}

func (h *cloudHub) live(runID string) (status, state string) {
	feed, ok := h.feed(runID)
	if !ok {
		return "", ""
	}
	_, run, state, ready := feed.current().Snapshot()
	if !ready {
		return run.Status, ""
	}
	return run.Status, state
}

func (h *cloudHub) agent(runID string) string {
	feed, ok := h.feed(runID)
	if !ok {
		return ""
	}
	_, run, _, _ := feed.current().Snapshot()
	return run.RuntimeAdapter
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
	var dropped, added []string
	for runID, feed := range h.feeds {
		if feed.background && runs[runID] == "" {
			feed.background = false
			dropped = append(dropped, runID)
		}
	}
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

func (s *Server) SyncPostHogCloud(ctx context.Context) {
	s.cloud.refreshBackground(ctx)
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
	h.mu.Lock()
	for _, task := range tasks {
		run := task.LatestRun
		if run == nil {
			continue
		}
		h.titles[run.ID] = strings.TrimSpace(task.Title)
		if !posthogcloud.Terminal(run.Status) && run.Status != "not_started" && len(runs) < postHogCloudBackgroundRuns {
			runs[run.ID] = task.ID
		}
	}
	h.mu.Unlock()
	h.setBackground(client, runs)
	h.server.mirror.sync(ctx, tasks)
}

func (h *cloudHub) knownTitle(runID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.titles[runID]
}

func (h *cloudHub) title(runID string) string {
	return cmp.Or(h.knownTitle(runID), "PostHog Cloud run")
}

func (f *cloudFeed) start() {
	follower := posthogcloud.NewFollower(f.client, f.taskID, f.runID, f.hub.server.options.PostHogCloudFollower, f.dispatch)
	f.mu.Lock()
	f.follower = follower
	f.running = true
	f.mu.Unlock()
	go func() {
		err := follower.Run(f.ctx)
		if err != nil && f.ctx.Err() == nil {
			f.hub.server.logger.Warn("stopped following a PostHog Cloud run", "run", f.runID, "error", err)
		}
		f.mu.Lock()
		if f.follower == follower {
			f.running = false
		}
		f.mu.Unlock()
	}()
}

func (f *cloudFeed) restart() {
	f.mu.Lock()
	running := f.running
	f.mu.Unlock()
	if !running && f.ctx.Err() == nil {
		f.start()
	}
}

func (f *cloudFeed) current() *posthogcloud.Follower {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.follower
}

func (f *cloudFeed) dispatch(update posthogcloud.Update) {
	if update.Credential != nil {
		go f.answerCredential(*update.Credential)
		return
	}
	f.mu.Lock()
	for sub := range f.subs {
		sub.offer(update)
	}
	notification, moved := f.noticeFor(update)
	f.mu.Unlock()
	if moved {
		go f.hub.server.mirror.touched(f.ctx, f.runID)
	}
	if update.Approvals {
		f.hub.changes.Notify()
	}
	if notification != nil && f.hub.server.hub != nil {
		f.hub.server.hub.Publish(*notification)
	}
}

func (f *cloudFeed) noticeFor(update posthogcloud.Update) (*notify.Notification, bool) {
	status := f.lastStatus
	if update.Run != nil {
		status = update.Run.Status
	}
	state := cmp.Or(update.State, f.lastState)
	previousState, previousStatus, baseline := f.lastState, f.lastStatus, f.baseline
	f.lastState, f.lastStatus = state, status
	moved := state != previousState || status != previousStatus
	if update.Reset {
		f.baseline = true
	}
	if !baseline || update.Reset || update.Err != nil {
		return nil, moved
	}
	var subtitle, body string
	switch {
	case status == "failed" && previousStatus != "failed":
		subtitle = "Failed"
		if update.Run != nil {
			body = update.Run.ErrorMessage
		}
	case state == posthogcloud.StateIdle && previousState == posthogcloud.StateWorking:
		subtitle = "Finished its turn"
		body = f.follower.Last(chat.KindAssistant)
	default:
		return nil, moved
	}
	now := time.Now().UTC()
	notification := &notify.Notification{
		Kind:        notify.KindCreated,
		ID:          postHogCloudApprovalPrefix + f.runID + ":" + strconv.FormatInt(now.UnixNano(), 36),
		Title:       f.hub.title(f.runID),
		Subtitle:    "PostHog Cloud · " + subtitle,
		Body:        chat.Truncate(strings.TrimSpace(body), 400),
		CreatedAt:   now.Format(time.RFC3339),
		PostHogTask: f.taskID,
		PostHogRun:  f.runID,
	}
	if mirrored, ok := f.hub.server.mirror.run(f.runID); ok {
		notification.WorkspaceID, notification.SurfaceID = mirrored.WorkspaceID, mirrored.SurfaceID
	}
	return notification, moved
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

func (h *cloudHub) approvals() []approvals.Approval {
	h.mu.Lock()
	feeds := make([]*cloudFeed, 0, len(h.feeds))
	for _, feed := range h.feeds {
		feeds = append(feeds, feed)
	}
	h.mu.Unlock()
	var pending []approvals.Approval
	for _, feed := range feeds {
		follower := feed.current()
		_, run, _, _ := follower.Snapshot()
		for _, request := range follower.Pending() {
			pending = append(pending, approvalFor(feed, run, request))
		}
	}
	return pending
}

func approvalFor(feed *cloudFeed, run posthogcloud.Run, request posthogcloud.PendingApproval) approvals.Approval {
	approval := approvals.Approval{
		ID:          postHogCloudApprovalPrefix + feed.runID + ":" + request.RequestID,
		Kind:        approvals.Kind(request.Kind),
		Agent:       cmp.Or(run.RuntimeAdapter, "claude"),
		Tool:        request.Tool,
		Detail:      request.Detail,
		Description: request.Title,
		Plan:        request.Plan,
		CreatedAt:   request.Time,
		PostHogTask: feed.taskID,
		PostHogRun:  feed.runID,
	}
	if mirrored, ok := feed.hub.server.mirror.run(feed.runID); ok {
		approval.WorkspaceID, approval.SurfaceID = mirrored.WorkspaceID, mirrored.SurfaceID
	}
	for _, question := range request.Questions {
		item := approvals.Question{Header: question.Header, Prompt: question.Question, MultiSelect: question.MultiSelect}
		for _, option := range question.Options {
			item.Options = append(item.Options, approvals.Option{Label: option.Label, Description: option.Description})
		}
		approval.Questions = append(approval.Questions, item)
	}
	return approval
}

func (h *cloudHub) answer(ctx context.Context, id string, answer approvals.Answer) error {
	runID, requestID, ok := strings.Cut(strings.TrimPrefix(id, postHogCloudApprovalPrefix), ":")
	if !ok {
		return approvals.ErrNotPending
	}
	feed, ok := h.feed(runID)
	if !ok {
		return approvals.ErrNotPending
	}
	follower := feed.current()
	var request *posthogcloud.PendingApproval
	for _, candidate := range follower.Pending() {
		if candidate.RequestID == requestID {
			request = &candidate
			break
		}
	}
	if request == nil {
		return approvals.ErrNotPending
	}
	params, err := permissionResponse(*request, answer)
	if err != nil {
		return err
	}
	result, err := feed.client.CommandWithRetry(ctx, feed.taskID, feed.runID, "permission_response", params)
	var failure *posthogcloud.CommandError
	if errors.As(err, &failure) && (failure.RunEnded() || failure.Code == "agent_error") {
		return approvals.ErrNotPending
	}
	if err != nil {
		return postHogCloudError(err)
	}
	if !strings.Contains(string(result), `"resolved":true`) {
		return approvals.ErrNotPending
	}
	follower.Resolve(requestID, params["optionId"].(string))
	return nil
}

func permissionResponse(request posthogcloud.PendingApproval, answer approvals.Answer) (map[string]any, error) {
	params := map[string]any{"requestId": request.RequestID}
	switch request.Kind {
	case posthogcloud.ApprovalQuestion:
		if len(answer.Answers) == 0 {
			return nil, approvals.InvalidAnswer("Pick an answer for each question.")
		}
		answers := map[string]string{}
		optionID := "option_0"
		for i, question := range request.Questions {
			if i >= len(answer.Answers) {
				break
			}
			answers[question.Question] = answer.Answers[i]
			if i == 0 {
				for index, option := range question.Options {
					if option.Label == answer.Answers[0] {
						optionID = "option_" + strconv.Itoa(index)
					}
				}
			}
		}
		params["optionId"] = optionID
		params["answers"] = answers
		return params, nil
	case posthogcloud.ApprovalPlan:
		wanted := map[string][]string{
			"manual":     {"default"},
			"autoAccept": {"acceptEdits", "auto"},
			"deny":       {"reject_with_feedback"},
		}[answer.Mode]
		if wanted == nil {
			return nil, approvals.InvalidAnswer("A plan answer needs mode manual, autoAccept or deny.")
		}
		optionID := optionByID(request.Options, wanted)
		if optionID == "" {
			optionID = optionByKind(request.Options, map[string][]string{"manual": {"allow_once", "allow_always"}, "autoAccept": {"allow_always", "allow_once"}, "deny": {"reject_once", "reject_always"}}[answer.Mode])
		}
		if optionID == "" {
			return nil, approvals.InvalidAnswer("The agent offered no matching choice.")
		}
		params["optionId"] = optionID
		if answer.Mode == "deny" && strings.TrimSpace(answer.Feedback) != "" {
			params["customInput"] = strings.TrimSpace(answer.Feedback)
		}
		return params, nil
	}
	kinds := map[string][]string{
		"once":   {"allow_once", "allow_always"},
		"always": {"allow_always", "allow_once"},
		"deny":   {"reject_once", "reject_always"},
	}[answer.Mode]
	if kinds == nil {
		return nil, approvals.InvalidAnswer("A permission answer needs mode once, always or deny.")
	}
	optionID := optionByKind(request.Options, kinds)
	if optionID == "" {
		return nil, approvals.InvalidAnswer("The agent offered no matching choice.")
	}
	params["optionId"] = optionID
	return params, nil
}

func optionByKind(options []posthogcloud.PermissionOption, kinds []string) string {
	for _, kind := range kinds {
		for _, option := range options {
			if option.Kind == kind {
				return option.OptionID
			}
		}
	}
	return ""
}

func optionByID(options []posthogcloud.PermissionOption, ids []string) string {
	for _, id := range ids {
		for _, option := range options {
			if option.OptionID == id {
				return id
			}
		}
	}
	return ""
}

func (h *cloudHub) sendMessage(ctx context.Context, client *posthogcloud.Client, taskID, runID, text string) error {
	run, err := client.Run(ctx, taskID, runID)
	if err != nil {
		return postHogCloudError(err)
	}
	params := map[string]any{"content": text}
	if run.Terminal() {
		if _, err := client.ResumeInCloud(ctx, taskID, runID); err != nil {
			return &requestError{Code: "posthog_cloud_resume_failed", Message: "PostHog couldn't reopen this run: " + err.Error()}
		}
		if feed, ok := h.feed(runID); ok {
			feed.restart()
		}
	} else if status, state := h.live(runID); status == "in_progress" && state == posthogcloud.StateWorking {
		params["steer"] = true
	}
	_, err = client.CommandWithRetry(ctx, taskID, runID, "user_message", params)
	var failure *posthogcloud.CommandError
	if errors.As(err, &failure) && failure.RunEnded() {
		return &requestError{Code: "posthog_cloud_run_ended", Message: "This run has ended and its sandbox is gone. Start a new run in PostHog."}
	}
	if err != nil {
		return postHogCloudError(err)
	}
	return nil
}

func (h *cloudHub) stopTurn(ctx context.Context, client *posthogcloud.Client, taskID, runID string) error {
	_, err := client.CommandWithRetry(ctx, taskID, runID, "cancel", map[string]any{})
	var failure *posthogcloud.CommandError
	if errors.As(err, &failure) && failure.RunEnded() {
		return nil
	}
	if err != nil {
		return postHogCloudError(err)
	}
	return nil
}

func (f *cloudFeed) answerCredential(request posthogcloud.CredentialRequest) {
	if request.Credential != "" && request.Credential != "claude_subscription_token" {
		return
	}
	f.mu.Lock()
	if f.answered[request.RequestID] {
		f.mu.Unlock()
		return
	}
	f.answered[request.RequestID] = true
	f.mu.Unlock()
	_, config, ok, err := f.hub.server.postHogCloudClient()
	if err != nil || !ok || config.ClaudeToken == "" {
		f.hub.server.logger.Info("a PostHog Cloud run asked for a Claude token, but none is saved", "run", f.runID)
		return
	}
	deadline := request.ExpiresAt
	if deadline.IsZero() || time.Until(deadline) > postHogCloudCredentialWindow {
		deadline = time.Now().Add(postHogCloudCredentialWindow)
	}
	ctx, cancel := context.WithDeadline(f.ctx, deadline)
	defer cancel()
	run, err := f.client.Run(ctx, f.taskID, f.runID)
	if err != nil || run.ClaudeSubscriptionUser() != config.UserID {
		return
	}
	params := map[string]any{"requestId": request.RequestID, "credential": "claude_subscription_token", "token": config.ClaudeToken}
	if _, err := f.client.CommandWithRetry(ctx, f.taskID, f.runID, "credential_response", params); err != nil {
		f.hub.server.logger.Warn("could not hand the Claude token to a PostHog Cloud run", "run", f.runID, "error", err)
		return
	}
	f.hub.server.logger.Info("handed the Claude token to a PostHog Cloud run", "run", f.runID)
}
