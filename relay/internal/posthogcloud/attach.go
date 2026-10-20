package posthogcloud

import "github.com/gantoine/muxer/relay/internal/chat"

const (
	AttachSnapshot = "snapshot"
	AttachResult   = "result"

	ActionSend   = "send"
	ActionStop   = "stop"
	ActionAnswer = "answer"
)

type AttachFrame struct {
	Type    string            `json:"type"`
	Ready   bool              `json:"ready,omitempty"`
	Entries []chat.Entry      `json:"entries,omitempty"`
	Status  string            `json:"status,omitempty"`
	State   string            `json:"state,omitempty"`
	Pending []PendingApproval `json:"pending,omitempty"`
	OK      bool              `json:"ok,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type AttachAction struct {
	Action string `json:"action"`
	Text   string `json:"text,omitempty"`
	Mode   string `json:"mode,omitempty"`
}
