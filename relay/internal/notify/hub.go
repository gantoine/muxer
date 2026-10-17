package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const historySize = 100

type Kind string

const (
	KindCreated   Kind = "created"
	KindDismissed Kind = "dismissed"
)

type Notification struct {
	Seq         int64  `json:"seq"`
	Kind        Kind   `json:"kind"`
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Subtitle    string `json:"subtitle,omitempty"`
	Body        string `json:"body,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	SurfaceID   string `json:"surface_id,omitempty"`
	TabTitle    string `json:"tab_title,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type Source interface {
	Notifications(ctx context.Context) ([]cmux.Notification, error)
}

type Hub struct {
	source Source
	logger *slog.Logger

	mu          sync.Mutex
	nextSeq     int64
	history     []Notification
	subscribers map[chan Notification]struct{}
}

func NewHub(source Source, logger *slog.Logger) *Hub {
	return &Hub{source: source, logger: logger, nextSeq: time.Now().UnixMilli(), subscribers: map[chan Notification]struct{}{}}
}

func (h *Hub) StreamResumed(context.Context) {}

func (h *Hub) HandleEvent(ctx context.Context, event cmux.Event) {
	var payload struct {
		NotificationID  string   `json:"notification_id"`
		NotificationIDs []string `json:"notification_ids"`
		ReplacedIDs     []string `json:"replaced_notification_ids"`
	}
	_ = json.Unmarshal(event.Payload, &payload)
	switch event.Name {
	case "notification.created":
		for _, replaced := range payload.ReplacedIDs {
			h.publish(Notification{Kind: KindDismissed, ID: replaced})
		}
		notification, ok := h.lookup(ctx, payload.NotificationID)
		if !ok {
			return
		}
		h.publish(Notification{
			Kind:        KindCreated,
			ID:          notification.ID,
			Title:       notification.Title,
			Subtitle:    notification.Subtitle,
			Body:        notification.Body,
			WorkspaceID: notification.WorkspaceID,
			SurfaceID:   notification.SurfaceID,
			TabTitle:    notification.TabTitle,
			CreatedAt:   notification.CreatedAt,
		})
	case "notification.read", "notification.removed", "notification.cleared":
		if payload.NotificationID != "" {
			payload.NotificationIDs = append(payload.NotificationIDs, payload.NotificationID)
		}
		for _, id := range payload.NotificationIDs {
			h.publish(Notification{Kind: KindDismissed, ID: id})
		}
	}
}

func (h *Hub) lookup(ctx context.Context, id string) (cmux.Notification, bool) {
	if id == "" {
		return cmux.Notification{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notifications, err := h.source.Notifications(callCtx)
	if err != nil {
		h.logger.Warn("could not read notification text", "error", err)
		return cmux.Notification{}, false
	}
	for _, notification := range notifications {
		if notification.ID == id {
			return notification, true
		}
	}
	return cmux.Notification{}, false
}

func (h *Hub) publish(notification Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextSeq++
	notification.Seq = h.nextSeq
	h.history = append(h.history, notification)
	if len(h.history) > historySize {
		h.history = h.history[len(h.history)-historySize:]
	}
	for subscriber := range h.subscribers {
		select {
		case subscriber <- notification:
		default:
		}
	}
}

func (h *Hub) LatestSeq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextSeq
}

func (h *Hub) Subscribe(afterSeq int64) (<-chan Notification, []Notification, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	channel := make(chan Notification, historySize)
	h.subscribers[channel] = struct{}{}
	var missed []Notification
	for _, notification := range h.history {
		if afterSeq > 0 && notification.Seq > afterSeq {
			missed = append(missed, notification)
		}
	}
	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, channel)
	}
	return channel, missed, unsubscribe
}
