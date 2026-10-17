package agents

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/pubsub"
)

const (
	retryGrace         = time.Minute
	expirySweep        = 10 * time.Second
	maxMessageBytes    = 32 * 1024
	maxSenderNameRunes = 64
	listLimit          = 50
	maxListedMessages  = 10
	senderPrefix       = "Muxer on "
)

var hooksThatEndRetries = map[string]bool{
	"UserPromptSubmit":  true,
	"PreToolUse":        true,
	"PermissionRequest": true,
	"AskUserQuestion":   true,
	"ExitPlanMode":      true,
	"Stop":              true,
	"SessionStart":      true,
	"SessionEnd":        true,
}

var hooksWhileBusy = map[string]bool{
	"UserPromptSubmit":   true,
	"PreToolUse":         true,
	"PostToolUse":        true,
	"PostToolUseFailure": true,
	"PreCompact":         true,
	"PostCompact":        true,
}

var hooksWhenIdle = map[string]bool{
	"Stop":         true,
	"StopFailure":  true,
	"SessionStart": true,
	"SessionEnd":   true,
}

var EventCategories = []string{"agent", "terminal"}

var programStatesWhileBusy = map[string]bool{
	"working": true,
	"blocked": true,
}

var ErrInvalidMessage = errors.New("a message needs 1 to 32768 bytes of text")

type Retry struct {
	SurfaceID   string    `json:"surface_id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Agent       string    `json:"agent,omitempty"`
	Attempt     int       `json:"attempt"`
	DueAt       time.Time `json:"due_at"`
}

type Source interface {
	SendAgentMessage(ctx context.Context, target, from, body string) (cmux.AgentMessage, error)
	AgentMessages(ctx context.Context, surfaceID string, limit int) ([]cmux.AgentMessage, error)
}

type Tracker struct {
	source Source
	logger *slog.Logger
	now    func() time.Time

	activity pubsub.Broadcaster

	mu                 sync.Mutex
	retries            map[string]Retry
	busy               map[string]bool
	messageSubscribers map[chan struct{}]string
}

func NewTracker(source Source, logger *slog.Logger) *Tracker {
	return &Tracker{
		source:             source,
		logger:             logger,
		now:                time.Now,
		retries:            map[string]Retry{},
		busy:               map[string]bool{},
		messageSubscribers: map[chan struct{}]string{},
	}
}

func (t *Tracker) StreamResumed(context.Context) {}

type retryPayload struct {
	Agent   string `json:"agent"`
	Attempt int    `json:"attempt"`
	DelayMS int64  `json:"delay_ms"`
}

func (t *Tracker) HandleEvent(_ context.Context, event cmux.Event) {
	surfaceID := event.Surface()
	if surfaceID == "" {
		return
	}
	hook, isHook := event.Hook()
	switch {
	case event.Name == "agent.auto_resume.scheduled":
		var payload retryPayload
		_ = json.Unmarshal(event.Payload, &payload)
		t.setRetry(Retry{
			SurfaceID:   surfaceID,
			WorkspaceID: event.Workspace(),
			Agent:       payload.Agent,
			Attempt:     payload.Attempt,
			DueAt:       t.now().Add(time.Duration(payload.DelayMS) * time.Millisecond).UTC(),
		})
	case event.Name == "agent.auto_resume.sent":
		t.clearRetry(surfaceID)
	case isHook:
		if hooksThatEndRetries[hook] {
			t.clearRetry(surfaceID)
		}
		switch {
		case hooksWhileBusy[hook]:
			t.setBusy(surfaceID, true)
		case hooksWhenIdle[hook]:
			t.setBusy(surfaceID, false)
		}
	case event.Name == "program_status.changed":
		if !programBusy(event.Payload) {
			t.setBusy(surfaceID, false)
		}
	case event.Name == "surface.closed":
		t.setBusy(surfaceID, false)
		t.clearRetry(surfaceID)
	case strings.HasPrefix(event.Name, "agent.message."):
		t.notifyMessages(surfaceID)
	}
}

func programBusy(payload json.RawMessage) bool {
	var status struct {
		Record *struct {
			State string `json:"state"`
		} `json:"record"`
	}
	_ = json.Unmarshal(payload, &status)
	return status.Record != nil && programStatesWhileBusy[status.Record.State]
}

func (t *Tracker) Run(ctx context.Context) {
	ticker := time.NewTicker(expirySweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := t.now().Add(-retryGrace)
			t.mu.Lock()
			before := len(t.retries)
			for surfaceID, retry := range t.retries {
				if retry.DueAt.Before(cutoff) {
					delete(t.retries, surfaceID)
				}
			}
			changed := len(t.retries) != before
			t.mu.Unlock()
			if changed {
				t.activity.Notify()
			}
		}
	}
}

func (t *Tracker) Retries() []Retry {
	t.mu.Lock()
	defer t.mu.Unlock()
	retries := make([]Retry, 0, len(t.retries))
	for _, retry := range t.retries {
		retries = append(retries, retry)
	}
	slices.SortFunc(retries, func(a, b Retry) int { return a.DueAt.Compare(b.DueAt) })
	return retries
}

func (t *Tracker) Busy() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	busy := make([]string, 0, len(t.busy))
	for surfaceID := range t.busy {
		busy = append(busy, surfaceID)
	}
	slices.Sort(busy)
	return busy
}

func (t *Tracker) SubscribeActivity() (<-chan struct{}, func()) {
	return t.activity.Subscribe()
}

func (t *Tracker) SubscribeMessages(surfaceID string) (<-chan struct{}, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	channel := make(chan struct{}, 1)
	t.messageSubscribers[channel] = surfaceID
	return channel, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.messageSubscribers, channel)
	}
}

func SenderName(deviceName string) string {
	name := strings.Join(strings.Fields(deviceName), " ")
	if name == "" {
		name = "a phone"
	}
	sender := senderPrefix + name
	if utf8.RuneCountInString(sender) > maxSenderNameRunes {
		sender = string([]rune(sender)[:maxSenderNameRunes])
	}
	return sender
}

func (t *Tracker) Send(ctx context.Context, surfaceID, deviceName, body string) (cmux.AgentMessage, error) {
	if strings.TrimSpace(body) == "" || len(body) > maxMessageBytes {
		return cmux.AgentMessage{}, ErrInvalidMessage
	}
	return t.source.SendAgentMessage(ctx, surfaceID, SenderName(deviceName), body)
}

func (t *Tracker) Messages(ctx context.Context, surfaceID string) ([]cmux.AgentMessage, error) {
	listed, err := t.source.AgentMessages(ctx, surfaceID, listLimit)
	if err != nil {
		return nil, err
	}
	fromPhones := slices.DeleteFunc(listed, func(message cmux.AgentMessage) bool {
		return !strings.HasPrefix(message.SenderName, senderPrefix)
	})
	return fromPhones[:min(len(fromPhones), maxListedMessages)], nil
}

func (t *Tracker) setRetry(retry Retry) {
	t.mu.Lock()
	t.retries[retry.SurfaceID] = retry
	t.mu.Unlock()
	t.activity.Notify()
}

func (t *Tracker) setBusy(surfaceID string, busy bool) {
	t.mu.Lock()
	changed := t.busy[surfaceID] != busy
	if busy {
		t.busy[surfaceID] = true
	} else {
		delete(t.busy, surfaceID)
	}
	t.mu.Unlock()
	if changed {
		t.activity.Notify()
	}
}

func (t *Tracker) clearRetry(surfaceID string) {
	t.mu.Lock()
	_, existed := t.retries[surfaceID]
	delete(t.retries, surfaceID)
	t.mu.Unlock()
	if existed {
		t.activity.Notify()
	}
}

func (t *Tracker) notifyMessages(surfaceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for subscriber, watched := range t.messageSubscribers {
		if watched == surfaceID {
			pubsub.Signal(subscriber)
		}
	}
}
