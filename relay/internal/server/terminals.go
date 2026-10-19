package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/host"
)

const maxTerminalTitleBytes = 256

type terminalRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
	Title       string `json:"title"`
	Force       bool   `json:"force"`
}

var errTerminalActionsUnsupported = unsupported("do that to a single terminal")

var errLastTerminal = &requestError{Code: "last_terminal", Message: "This is the workspace's only terminal. Close the workspace instead."}

func (s *Server) terminalRequest(ctx context.Context, kind string, raw json.RawMessage, act func(context.Context, terminalRequest) error) error {
	var request terminalRequest
	if err := json.Unmarshal(raw, &request); err != nil || request.WorkspaceID == "" || request.SurfaceID == "" {
		return invalid(kind + " needs workspace_id and surface_id.")
	}
	return act(ctx, request)
}

func (s *Server) terminalAction(method string, call func() error) error {
	err := guard(s, method, errTerminalActionsUnsupported, call)
	if !errors.Is(err, errTerminalActionsUnsupported) {
		s.workspaces.refresh()
	}
	return err
}

func (s *Server) closeTerminal(ctx context.Context, request terminalRequest) error {
	err := s.terminalAction(cmux.MethodCloseTerminal, func() error {
		return s.cmux.CloseTerminal(ctx, request.WorkspaceID, request.SurfaceID, request.Force)
	})
	if cmux.IsLastSurface(err) {
		return errLastTerminal
	}
	return err
}

func (s *Server) renameTerminal(ctx context.Context, request terminalRequest) error {
	title := strings.TrimSpace(request.Title)
	if len(title) > maxTerminalTitleBytes {
		return invalid(fmt.Sprintf("A terminal name can be up to %d bytes.", maxTerminalTitleBytes))
	}
	return s.terminalAction(cmux.MethodRenameTerminal, func() error {
		return s.cmux.RenameTerminal(ctx, request.WorkspaceID, request.SurfaceID, title)
	})
}

func (s *Server) restartTerminal(ctx context.Context, request terminalRequest) error {
	return s.terminalAction(cmux.MethodRestartTerminal, func() error {
		return s.cmux.RestartTerminal(ctx, request.WorkspaceID, request.SurfaceID, host.LoginShell())
	})
}

func (s *session) watchInputState(ctx context.Context, current *attachment) {
	var last *cmux.InputState
	for {
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		state, err := guarded(s.server, cmux.MethodInputState, errMethodMissing, func() (cmux.InputState, error) {
			return s.server.cmux.InputState(callCtx, current.workspaceID, current.surfaceID)
		})
		cancel()
		if ctx.Err() != nil || errors.Is(err, errMethodMissing) {
			return
		}
		if err == nil && (last == nil || *last != state) {
			last = &state
			s.send(map[string]any{
				"type":             "input_state",
				"surface_id":       current.surfaceID,
				"state":            state.State,
				"blocks_typing":    state.BlocksTyping,
				"waiting_on_human": state.WaitingOnHuman,
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.server.options.PollInterval.InputState):
		}
	}
}
