package posthogcloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/chat"
)

const (
	DefaultIdleTimeout = 90 * time.Second
	DefaultRetryBase   = 500 * time.Millisecond
	maxRetryDelay      = 30 * time.Second
	healthyConnection  = time.Minute
	streamBuffer       = 1024
	maxSeenEventIDs    = 20000
)

var (
	errRotated     = errors.New("the stream rotated")
	errResync      = errors.New("the stream lost its place")
	errStreamEnded = errors.New("the stream ended")
	errIdle        = errors.New("the stream went quiet")
)

type StreamError struct {
	Message string
}

func (e *StreamError) Error() string {
	return "the stream failed: " + e.Message
}

type Update struct {
	Reset      bool
	Entries    []chat.Entry
	Run        *Run
	State      string
	Approvals  bool
	Credential *CredentialRequest
	Err        error
}

type CredentialRequest struct {
	RequestID  string    `json:"requestId"`
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type FollowerOptions struct {
	IdleTimeout time.Duration
	RetryBase   time.Duration
}

type Follower struct {
	client  *Client
	taskID  string
	runID   string
	emit    func(Update)
	options FollowerOptions

	mu         sync.Mutex
	transcript *Transcript
	run        Run
	ready      bool

	coverage     coverage
	bootstrapped bool
	leg          string
	lastEventID  string
	seen         map[string]bool
}

func NewFollower(client *Client, taskID, runID string, options FollowerOptions, emit func(Update)) *Follower {
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = DefaultIdleTimeout
	}
	if options.RetryBase <= 0 {
		options.RetryBase = DefaultRetryBase
	}
	return &Follower{client: client, taskID: taskID, runID: runID, emit: emit, options: options, transcript: NewTranscript(), seen: map[string]bool{}}
}

func (f *Follower) Snapshot() (entries []chat.Entry, run Run, state string, ready bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transcript.Entries(), f.run, f.transcript.State(), f.ready
}

func (f *Follower) Pending() []PendingApproval {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transcript.Pending()
}

func (f *Follower) Last(kind string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries := f.transcript.Entries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == kind {
			return entries[i].Text
		}
	}
	return ""
}

func (f *Follower) Resolve(requestID, optionID string) {
	f.feed(func(t *Transcript) []chat.Entry { return t.Resolve(requestID, optionID) })
}

func (f *Follower) Run(ctx context.Context) error {
	delay := f.options.RetryBase
	for {
		run, err := f.client.Run(ctx, f.taskID, f.runID)
		if err == nil {
			f.setRun(run)
			if run.Terminal() {
				return f.finish(ctx)
			}
			started := time.Now()
			err = f.live(ctx)
			if errors.Is(err, errStreamEnded) {
				return f.finish(ctx)
			}
			if errors.Is(err, errRotated) || time.Since(started) > healthyConnection {
				delay = f.options.RetryBase
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, errRotated) && !errors.Is(err, errResync) {
			f.emit(Update{Err: err})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if !errors.Is(err, errRotated) {
			delay = min(delay*2, maxRetryDelay)
		}
	}
}

type streamItem struct {
	event Event
	err   error
}

func (f *Follower) live(ctx context.Context) error {
	token, err := f.client.StreamToken(ctx, f.taskID, f.runID)
	if err != nil {
		return err
	}
	if leg := token.Leg(); leg != f.leg {
		f.leg = leg
		f.lastEventID = ""
		f.bootstrapped = false
		f.seen = map[string]bool{}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := f.client.OpenStream(streamCtx, f.taskID, f.runID, token, f.lastEventID)
	if err != nil {
		return err
	}
	defer stream.Close()
	items := make(chan streamItem, streamBuffer)
	go func() {
		for {
			event, err := stream.Next()
			select {
			case items <- streamItem{event: event, err: err}:
			case <-streamCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	if !f.bootstrapped {
		if err := f.bootstrap(ctx); err != nil {
			return err
		}
	}
	idle := time.NewTimer(f.options.IdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return errIdle
		case item := <-items:
			if !idle.Stop() {
				<-idle.C
			}
			idle.Reset(f.options.IdleTimeout)
			if item.err != nil {
				if errors.Is(item.err, io.EOF) {
					return errRotated
				}
				return item.err
			}
			if err := f.handle(item.event); err != nil {
				return err
			}
		}
	}
}

func (f *Follower) handle(event Event) error {
	if event.ID != "" {
		f.lastEventID = event.ID
	}
	switch event.Name {
	case "keepalive":
		return nil
	case "error":
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(event.Data, &failure)
		return &StreamError{Message: firstNonEmpty(failure.Error, "unknown error")}
	case "stream-end":
		return errStreamEnded
	case "end":
		var end struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(event.Data, &end)
		if end.Type == "resync" {
			f.lastEventID = ""
			f.bootstrapped = false
			return errResync
		}
		return errRotated
	}
	if event.ID != "" {
		if f.seen[event.ID] {
			return nil
		}
		if len(f.seen) >= maxSeenEventIDs {
			f.seen = map[string]bool{}
		}
		f.seen[event.ID] = true
	}
	var frame struct {
		Type         string          `json:"type"`
		Timestamp    string          `json:"timestamp"`
		EventID      string          `json:"event_id"`
		Notification json.RawMessage `json:"notification"`
	}
	if json.Unmarshal(event.Data, &frame) != nil {
		return nil
	}
	switch frame.Type {
	case "task_run_state":
		var state Run
		if json.Unmarshal(event.Data, &state) == nil {
			f.mu.Lock()
			run := f.run
			run.Status, run.Stage, run.Branch, run.ErrorMessage = state.Status, state.Stage, state.Branch, state.ErrorMessage
			if state.Output != nil {
				run.Output = state.Output
			}
			f.mu.Unlock()
			f.setRun(run)
			if run.Terminal() {
				return errStreamEnded
			}
		}
		return nil
	case "permission_request":
		f.feed(func(t *Transcript) []chat.Entry { return t.FeedPermissionRequest(event.Data, frame.Timestamp) })
		return nil
	case "credential_request":
		var request CredentialRequest
		if json.Unmarshal(event.Data, &request) == nil && request.RequestID != "" {
			f.emit(Update{Credential: &request})
		}
		return nil
	}
	if len(frame.Notification) == 0 || f.coverage.covers(frame.EventID) {
		return nil
	}
	f.feed(func(t *Transcript) []chat.Entry { return t.Feed(event.Data) })
	return nil
}

func (f *Follower) feed(apply func(*Transcript) []chat.Entry) {
	f.mu.Lock()
	before := f.transcript.State()
	pendingBefore := requestIDs(f.transcript.Pending())
	changed := apply(f.transcript)
	state := f.transcript.State()
	approvalsChanged := requestIDs(f.transcript.Pending()) != pendingBefore
	run := f.run
	f.mu.Unlock()
	if len(changed) == 0 && state == before && !approvalsChanged {
		return
	}
	update := Update{Entries: changed, State: state, Approvals: approvalsChanged}
	if state != before {
		update.Run = &run
	}
	f.emit(update)
}

func (f *Follower) bootstrap(ctx context.Context) error {
	if err := f.reload(ctx); err != nil {
		return err
	}
	f.bootstrapped = true
	return nil
}

func (f *Follower) finish(ctx context.Context) error {
	if run, err := f.client.Run(ctx, f.taskID, f.runID); err == nil {
		f.mu.Lock()
		f.run = run
		f.mu.Unlock()
	}
	return f.reload(ctx)
}

func (f *Follower) reload(ctx context.Context) error {
	transcript := NewTranscript()
	var covered coverage
	offset := 0
	for {
		entries, more, err := f.client.SessionLogs(ctx, f.taskID, f.runID, offset)
		if err != nil {
			return err
		}
		for _, raw := range entries {
			transcript.Feed(raw)
			covered.add(raw)
		}
		offset += len(entries)
		if !more || len(entries) == 0 {
			break
		}
	}
	f.mu.Lock()
	f.transcript = transcript
	f.coverage = covered
	f.ready = true
	run := f.run
	entries := transcript.Entries()
	state := transcript.State()
	f.mu.Unlock()
	f.emit(Update{Reset: true, Entries: entries, Run: &run, State: state, Approvals: true})
	return nil
}

func requestIDs(pending []PendingApproval) string {
	ids := make([]string, len(pending))
	for i, approval := range pending {
		ids[i] = approval.RequestID
	}
	return strings.Join(ids, ",")
}

func (f *Follower) setRun(run Run) {
	f.mu.Lock()
	changed := !sameRun(f.run, run)
	f.run = run
	state := f.transcript.State()
	f.mu.Unlock()
	if changed {
		f.emit(Update{Run: &run, State: state})
	}
}

func sameRun(a, b Run) bool {
	return a.ID == b.ID && a.Status == b.Status && a.Stage == b.Stage && a.Branch == b.Branch &&
		a.ErrorMessage == b.ErrorMessage && a.PullRequestURL() == b.PullRequestURL() && a.Model == b.Model && a.RuntimeAdapter == b.RuntimeAdapter
}

var agentEventID = regexp.MustCompile(`^(.+)-(\d+)$`)

type eventRange struct {
	first, last int
}

type coverage struct {
	ids    map[string]bool
	ranges map[string][]eventRange
}

func (c *coverage) add(raw []byte) {
	var entry struct {
		EventID      string `json:"event_id"`
		FirstEventID string `json:"first_event_id"`
	}
	if json.Unmarshal(raw, &entry) != nil || entry.EventID == "" {
		return
	}
	if c.ids == nil {
		c.ids = map[string]bool{}
		c.ranges = map[string][]eventRange{}
	}
	c.ids[entry.EventID] = true
	if entry.FirstEventID == "" {
		return
	}
	c.ids[entry.FirstEventID] = true
	firstBoot, first, ok1 := splitEventID(entry.FirstEventID)
	lastBoot, last, ok2 := splitEventID(entry.EventID)
	if ok1 && ok2 && firstBoot == lastBoot && first <= last {
		c.ranges[firstBoot] = append(c.ranges[firstBoot], eventRange{first, last})
	}
}

func (c *coverage) covers(eventID string) bool {
	if eventID == "" || c.ids == nil {
		return false
	}
	if c.ids[eventID] {
		return true
	}
	boot, sequence, ok := splitEventID(eventID)
	if !ok {
		return false
	}
	for _, r := range c.ranges[boot] {
		if r.first <= sequence && sequence <= r.last {
			return true
		}
	}
	return false
}

func splitEventID(eventID string) (string, int, bool) {
	match := agentEventID.FindStringSubmatch(eventID)
	if match == nil {
		return "", 0, false
	}
	sequence, err := strconv.Atoi(match[2])
	return match[1], sequence, err == nil
}
