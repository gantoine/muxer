package approvals

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/claude"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/pubsub"
)

type Kind string

const (
	KindPermission Kind = "permission"
	KindQuestion   Kind = "question"
	KindPlan       Kind = "plan"
)

const (
	feedKindPermission = "permissionRequest"
	feedKindQuestion   = "question"
	feedKindPlan       = "exitPlan"
	feedStatusPending  = "pending"
	phaseReceived      = "received"
	phaseCompleted     = "completed"
	maxDetailLength    = 8000
	lookupAttempts     = 5
	lookupDelay        = 200 * time.Millisecond
	sweepInterval      = 15 * time.Second
	callTimeout        = 15 * time.Second
)

var actionableHooks = map[string]bool{
	"PermissionRequest": true,
	"AskUserQuestion":   true,
	"ExitPlanMode":      true,
}

var permissionModes = map[string]bool{"once": true, "always": true, "deny": true}

var planModes = map[string]bool{"manual": true, "autoAccept": true, "deny": true}

const maxFeedbackLength = 4000

var ErrNotPending = errors.New("that request was already answered or timed out")

type InvalidAnswer string

func (e InvalidAnswer) Error() string { return string(e) }

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Question struct {
	Header      string   `json:"header,omitempty"`
	Prompt      string   `json:"prompt"`
	MultiSelect bool     `json:"multi_select"`
	Options     []Option `json:"options"`
}

type Approval struct {
	ID          string     `json:"id"`
	Kind        Kind       `json:"kind"`
	Agent       string     `json:"agent,omitempty"`
	WorkspaceID string     `json:"workspace_id"`
	SurfaceID   string     `json:"surface_id"`
	Tool        string     `json:"tool,omitempty"`
	Detail      string     `json:"detail,omitempty"`
	Description string     `json:"description,omitempty"`
	Questions   []Question `json:"questions,omitempty"`
	Plan        string     `json:"plan,omitempty"`
	Cwd         string     `json:"cwd,omitempty"`
	CreatedAt   string     `json:"created_at,omitempty"`
}

type Answer struct {
	Mode     string   `json:"mode,omitempty"`
	Answers  []string `json:"answers,omitempty"`
	Feedback string   `json:"feedback,omitempty"`
}

type Source interface {
	FeedItems(ctx context.Context) ([]cmux.FeedItem, error)
	ReplyPermission(ctx context.Context, requestID, mode string) error
	ReplyQuestion(ctx context.Context, requestID string, selections []string) error
	ReplyExitPlan(ctx context.Context, requestID, mode, feedback string) error
}

type Tracker struct {
	source Source
	logger *slog.Logger

	mu      sync.Mutex
	pending []Approval
	changes pubsub.Broadcaster
}

func NewTracker(source Source, logger *slog.Logger) *Tracker {
	return &Tracker{source: source, logger: logger}
}

type feedPayload struct {
	RequestID   string `json:"_opencode_request_id"`
	Source      string `json:"_source"`
	Phase       string `json:"phase"`
	WorkspaceID string `json:"-"`
	SurfaceID   string `json:"-"`
}

func (t *Tracker) HandleEvent(ctx context.Context, event cmux.Event) {
	hook, ok := event.Hook()
	if !ok {
		return
	}
	var payload feedPayload
	_ = json.Unmarshal(event.Payload, &payload)
	payload.WorkspaceID, payload.SurfaceID = event.Workspace(), event.Surface()
	switch payload.Phase {
	case phaseReceived:
		if actionableHooks[hook] && payload.RequestID != "" {
			t.track(ctx, payload)
		} else if t.hasPendingOn(payload.SurfaceID) {
			t.reconcile(ctx)
		}
	case phaseCompleted:
		t.remove(payload.RequestID)
	}
}

func (t *Tracker) track(ctx context.Context, payload feedPayload) {
	for attempt := 0; attempt < lookupAttempts; attempt++ {
		items, err := t.feedItems(ctx)
		if err != nil {
			t.logger.Warn("could not read the cmux feed", "error", err)
			return
		}
		for _, item := range items {
			if item.RequestID != payload.RequestID {
				continue
			}
			if item.Status != feedStatusPending {
				return
			}
			if approval, ok := approvalFrom(item, payload); ok {
				t.add(approval)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(lookupDelay):
		}
	}
}

func approvalFrom(item cmux.FeedItem, payload feedPayload) (Approval, bool) {
	approval := Approval{
		ID:          item.RequestID,
		Agent:       cmp.Or(item.Source, payload.Source),
		WorkspaceID: payload.WorkspaceID,
		SurfaceID:   payload.SurfaceID,
		Cwd:         item.Cwd,
		CreatedAt:   item.CreatedAt,
	}
	switch item.Kind {
	case feedKindPermission:
		approval.Kind = KindPermission
		approval.Tool = cmp.Or(item.ToolName, item.Title)
		approval.Detail, approval.Description = describeToolInput(item.ToolInput)
	case feedKindQuestion:
		approval.Kind = KindQuestion
		for _, question := range item.Questions {
			converted := Question{Header: question.Header, Prompt: question.Prompt, MultiSelect: question.MultiSelect, Options: []Option{}}
			for _, option := range question.Options {
				converted.Options = append(converted.Options, Option{Label: option.Label, Description: option.Description})
			}
			approval.Questions = append(approval.Questions, converted)
		}
		if len(approval.Questions) == 0 {
			return Approval{}, false
		}
	case feedKindPlan:
		approval.Kind = KindPlan
		approval.Plan = planText(item.Plan)
	default:
		return Approval{}, false
	}
	return approval, true
}

func planText(raw string) string {
	var input struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err == nil && input.Plan != "" {
		return input.Plan
	}
	return raw
}

func describeToolInput(raw string) (detail, description string) {
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return claude.Truncate(raw, maxDetailLength), ""
	}
	description, _ = input["description"].(string)
	delete(input, "description")
	if _, value := claude.ToolInputValue(input); value != "" {
		return claude.Truncate(value, maxDetailLength), description
	}
	if len(input) == 0 {
		return "", description
	}
	pretty, _ := json.MarshalIndent(input, "", "  ")
	return claude.Truncate(string(pretty), maxDetailLength), description
}

func (t *Tracker) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.reconcile(ctx)
		}
	}
}

func (t *Tracker) StreamResumed(ctx context.Context) {
	t.reconcile(ctx)
}

func (t *Tracker) reconcile(ctx context.Context) {
	checked := t.Pending()
	if len(checked) == 0 {
		return
	}
	items, err := t.feedItems(ctx)
	if err != nil {
		t.logger.Warn("could not read the cmux feed", "error", err)
		return
	}
	stillPending := map[string]bool{}
	for _, item := range items {
		if item.Status == feedStatusPending && item.RequestID != "" {
			stillPending[item.RequestID] = true
		}
	}
	settled := map[string]bool{}
	for _, approval := range checked {
		if !stillPending[approval.ID] {
			settled[approval.ID] = true
		}
	}
	t.removeWhere(func(approval Approval) bool { return settled[approval.ID] })
}

func (t *Tracker) feedItems(ctx context.Context) ([]cmux.FeedItem, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return t.source.FeedItems(callCtx)
}

func (t *Tracker) Answer(ctx context.Context, id string, answer Answer) error {
	approval, ok := t.find(id)
	if !ok {
		return ErrNotPending
	}
	var err error
	switch approval.Kind {
	case KindPermission:
		if !permissionModes[answer.Mode] {
			return InvalidAnswer("mode must be once, always or deny")
		}
		err = t.source.ReplyPermission(ctx, id, answer.Mode)
	case KindQuestion:
		if len(answer.Answers) != len(approval.Questions) {
			return InvalidAnswer("send one answer per question")
		}
		err = t.source.ReplyQuestion(ctx, id, answer.Answers)
	case KindPlan:
		if !planModes[answer.Mode] {
			return InvalidAnswer("mode must be manual, autoAccept or deny")
		}
		if answer.Mode == "autoAccept" && answer.Feedback != "" {
			return InvalidAnswer("feedback goes with manual or deny")
		}
		if len(answer.Feedback) > maxFeedbackLength {
			return InvalidAnswer("feedback must be at most 4000 bytes")
		}
		err = t.source.ReplyExitPlan(ctx, id, answer.Mode, answer.Feedback)
	}
	if err == nil {
		t.remove(id)
	}
	return err
}

func (t *Tracker) Pending() []Approval {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.pending)
}

func (t *Tracker) Subscribe() (<-chan struct{}, func()) {
	return t.changes.Subscribe()
}

func (t *Tracker) find(id string) (Approval, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	index := slices.IndexFunc(t.pending, func(approval Approval) bool { return approval.ID == id })
	if index < 0 {
		return Approval{}, false
	}
	return t.pending[index], true
}

func (t *Tracker) hasPendingOn(surfaceID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.ContainsFunc(t.pending, func(approval Approval) bool { return approval.SurfaceID == surfaceID })
}

func (t *Tracker) add(approval Approval) {
	t.mu.Lock()
	if slices.ContainsFunc(t.pending, func(existing Approval) bool { return existing.ID == approval.ID }) {
		t.mu.Unlock()
		return
	}
	t.pending = append(t.pending, approval)
	t.mu.Unlock()
	t.changes.Notify()
}

func (t *Tracker) remove(id string) {
	if id == "" {
		return
	}
	t.removeWhere(func(approval Approval) bool { return approval.ID == id })
}

func (t *Tracker) removeWhere(settled func(Approval) bool) {
	t.mu.Lock()
	before := len(t.pending)
	t.pending = slices.DeleteFunc(t.pending, settled)
	changed := len(t.pending) != before
	t.mu.Unlock()
	if changed {
		t.changes.Notify()
	}
}
