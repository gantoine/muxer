package server

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/claude"
	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/codex"
	"github.com/gantoine/muxer/relay/internal/pi"
)

const (
	agentKindClaude         = "claude"
	agentKindCodex          = "codex"
	agentKindPi             = "pi"
	maxInitialEntries       = 300
	initialTranscriptWindow = 4 << 20
)

var chatAgents = []string{agentKindClaude, agentKindCodex, agentKindPi}

type transcript interface {
	Feed(raw []byte) []chat.Entry
	Entries() []chat.Entry
}

type chatWatch struct {
	agent      cmux.AgentSession
	transcript transcript
	tail       *chat.Tail
	sent       bool
	sentEmpty  bool
}

func (s *session) watchChat(ctx context.Context, current *attachment) {
	if runID, mirrored, ok := s.server.mirror.runForSurface(current.surfaceID); ok {
		s.followMirroredChat(ctx, current, mirrored.TaskID, runID)
		return
	}
	var watch chatWatch
	announced := false
	for {
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		agent, err := s.server.chatAgent(callCtx, current.workspaceID, current.surfaceID)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if !announced || agent.SessionID != watch.agent.SessionID {
				announced = true
				watch = chatWatch{agent: agent}
				s.sendChatSession(current, agent.SessionID)
			}
			if agent.SessionID != "" {
				s.followTranscript(current, &watch)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.server.options.PollInterval.Chat):
		}
	}
}

func (s *Server) agentSession(ctx context.Context, workspaceID, surfaceID string) (cmux.AgentSession, error) {
	if running, ok := s.options.ClaudeSession(surfaceID); ok && running.SessionID != "" {
		return cmux.AgentSession{Kind: agentKindClaude, SessionID: running.SessionID, Cwd: running.Cwd}, nil
	}
	agent, err := guarded(s, cmux.MethodAgentSession, errMethodMissing, func() (cmux.AgentSession, error) {
		return s.cmux.AgentSession(ctx, workspaceID, surfaceID)
	})
	if errors.Is(err, errMethodMissing) {
		return cmux.AgentSession{}, nil
	}
	if err != nil || agent.SessionID == "" || s.agentRunning(ctx, workspaceID, surfaceID) {
		return agent, err
	}
	return cmux.AgentSession{}, nil
}

func (s *Server) agentRunning(ctx context.Context, workspaceID, surfaceID string) bool {
	state, err := guarded(s, cmux.MethodInputState, errMethodMissing, func() (cmux.InputState, error) {
		return s.cmux.InputState(ctx, workspaceID, surfaceID)
	})
	return err != nil || state.Agent
}

func (s *Server) chatAgent(ctx context.Context, workspaceID, surfaceID string) (cmux.AgentSession, error) {
	agent, err := s.agentSession(ctx, workspaceID, surfaceID)
	if err != nil || !slices.Contains(chatAgents, agent.Kind) {
		return cmux.AgentSession{}, err
	}
	return agent, nil
}

func (s *Server) transcriptPath(agent cmux.AgentSession) (string, bool) {
	switch agent.Kind {
	case agentKindCodex:
		return codex.FindRollout(s.options.CodexHome, agent.SessionID)
	case agentKindPi:
		return pi.FindSession(s.options.PiAgentDir, agent.SessionID)
	}
	return claude.FindTranscript(s.options.ClaudeConfigDir, agent.Cwd, agent.SessionID)
}

func newTranscript(kind string) transcript {
	switch kind {
	case agentKindCodex:
		return codex.NewTranscript()
	case agentKindPi:
		return pi.NewTranscript()
	}
	return claude.NewTranscript()
}

func (s *session) followTranscript(current *attachment, watch *chatWatch) {
	if watch.tail == nil {
		path, ok := s.server.transcriptPath(watch.agent)
		if !ok {
			if !watch.sentEmpty {
				watch.sentEmpty = true
				s.sendChat(current, watch.agent.SessionID, nil, true)
			}
			return
		}
		watch.tail = chat.NewTail(path, initialTranscriptWindow)
		watch.transcript = newTranscript(watch.agent.Kind)
	}
	lines, restarted, err := watch.tail.Lines()
	if err != nil {
		s.server.logger.Warn("could not read the agent transcript", "agent", watch.agent.Kind, "error", err)
		return
	}
	if restarted {
		watch.transcript = newTranscript(watch.agent.Kind)
		watch.sent = false
	}
	var changed []chat.Entry
	for _, line := range lines {
		changed = append(changed, watch.transcript.Feed(line)...)
	}
	if !watch.sent {
		entries := watch.transcript.Entries()
		if len(entries) > maxInitialEntries {
			entries = entries[len(entries)-maxInitialEntries:]
		}
		watch.sent = true
		s.sendChat(current, watch.agent.SessionID, entries, true)
		return
	}
	if len(changed) > 0 {
		s.sendChat(current, watch.agent.SessionID, changed, false)
	}
}

func (s *session) sendChatSession(current *attachment, sessionID string) {
	var id any
	if sessionID != "" {
		id = sessionID
	}
	s.send(map[string]any{"type": "chat_session", "surface_id": current.surfaceID, "session_id": id})
}

func (s *session) sendChat(current *attachment, sessionID string, entries []chat.Entry, reset bool) {
	if entries == nil {
		entries = []chat.Entry{}
	}
	s.send(map[string]any{
		"type":       "chat",
		"surface_id": current.surfaceID,
		"session_id": sessionID,
		"reset":      reset,
		"entries":    entries,
	})
}
