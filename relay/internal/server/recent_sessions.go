package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const (
	defaultRecentSessions = 50
	maxRecentSessions     = 200
	maxSessionQueryBytes  = 256
	terminalReadyTimeout  = 8 * time.Second
	terminalReadyPoll     = 200 * time.Millisecond
)

var bareSlashCommand = regexp.MustCompile(`^/\S+$`)

var errRecentSessionsUnsupported = unsupported("list past agent sessions")

var errSessionNotFound = &requestError{Code: "not_found", Message: "cmux no longer has that session."}

var errSessionNotResumable = &requestError{Code: "not_resumable", Message: "cmux doesn't know how to resume that session."}

type recentSession struct {
	Agent        string `json:"agent"`
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	Cwd          string `json:"cwd,omitempty"`
	GitBranch    string `json:"git_branch,omitempty"`
	Created      string `json:"created,omitempty"`
	Modified     string `json:"modified"`
	MessageCount *int   `json:"message_count,omitempty"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	SurfaceID    string `json:"surface_id,omitempty"`
}

type terminalRef struct {
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
}

type resumedSession struct {
	terminalRef
	AlreadyOpen bool `json:"already_open"`
}

type sessionQuery struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func parseSessionQuery(raw json.RawMessage) (sessionQuery, error) {
	var request sessionQuery
	_ = json.Unmarshal(raw, &request)
	request.Query = strings.TrimSpace(request.Query)
	if len(request.Query) > maxSessionQueryBytes {
		return request, invalid(fmt.Sprintf("A search can be up to %d bytes.", maxSessionQueryBytes))
	}
	if request.Limit <= 0 {
		request.Limit = defaultRecentSessions
	}
	request.Limit = min(request.Limit, maxRecentSessions)
	return request, nil
}

func (s *Server) recentSessions(ctx context.Context, request sessionQuery) ([]recentSession, error) {
	var found []cmux.VaultSession
	var err error
	if request.Query == "" {
		found, err = guarded(s, cmux.MethodVaultSessions, errRecentSessionsUnsupported, func() ([]cmux.VaultSession, error) {
			return s.cmux.VaultSessions(ctx, "", request.Limit)
		})
	} else {
		found, err = guarded(s, cmux.MethodVaultSearch, errRecentSessionsUnsupported, func() ([]cmux.VaultSession, error) {
			return s.cmux.VaultSearch(ctx, request.Query, request.Limit)
		})
	}
	if err != nil {
		return nil, err
	}
	open := s.openSessions(ctx)
	sessions := make([]recentSession, 0, len(found))
	listed := map[[2]string]bool{}
	for _, entry := range found {
		key := [2]string{entry.Agent, entry.SessionID}
		if listed[key] {
			continue
		}
		listed[key] = true
		session := recentSession{
			Agent:        entry.Agent,
			SessionID:    entry.SessionID,
			Title:        sessionTitle(entry.Title),
			Cwd:          entry.Cwd,
			GitBranch:    entry.GitBranch,
			Created:      entry.Created,
			Modified:     entry.Modified,
			MessageCount: entry.MessageCount,
		}
		if terminal, ok := open[entry.SessionID]; ok {
			session.WorkspaceID = terminal.WorkspaceID
			session.SurfaceID = terminal.SurfaceID
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func sessionTitle(title string) string {
	title, _, _ = strings.Cut(strings.TrimSpace(title), "\n")
	title = strings.TrimSpace(title)
	if bareSlashCommand.MatchString(title) {
		return ""
	}
	return title
}

func (s *Server) openSessions(ctx context.Context) map[string]terminalRef {
	list, err := s.cmux.Workspaces(ctx)
	if err != nil {
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	open := map[string]terminalRef{}
	for _, workspace := range list.Workspaces {
		for _, terminal := range workspace.Terminals {
			if terminal.AgentSource == nil {
				continue
			}
			ref := terminalRef{WorkspaceID: workspace.ID, SurfaceID: terminal.ID}
			wg.Go(func() {
				agent, _ := s.agentSession(ctx, ref.WorkspaceID, ref.SurfaceID)
				if agent.SessionID == "" {
					return
				}
				mu.Lock()
				open[agent.SessionID] = ref
				mu.Unlock()
			})
		}
	}
	wg.Wait()
	return open
}

func (s *Server) findSession(ctx context.Context, agent, sessionID string) (cmux.VaultSession, error) {
	listed, err := guarded(s, cmux.MethodVaultSessions, errRecentSessionsUnsupported, func() ([]cmux.VaultSession, error) {
		return s.cmux.VaultSessions(ctx, agent, maxRecentSessions)
	})
	if err != nil {
		return cmux.VaultSession{}, err
	}
	for _, entry := range listed {
		if entry.SessionID == sessionID {
			return entry, nil
		}
	}
	searched, err := guarded(s, cmux.MethodVaultSearch, errSessionNotFound, func() ([]cmux.VaultSession, error) {
		return s.cmux.VaultSearch(ctx, sessionID, maxRecentSessions)
	})
	if err != nil {
		return cmux.VaultSession{}, err
	}
	for _, entry := range searched {
		if entry.Agent == agent && entry.SessionID == sessionID {
			return entry, nil
		}
	}
	return cmux.VaultSession{}, errSessionNotFound
}

func (s *Server) resumeSession(ctx context.Context, agent, sessionID string) (resumedSession, error) {
	entry, err := s.findSession(ctx, agent, sessionID)
	if err != nil {
		return resumedSession{}, err
	}
	if terminal, ok := s.openSessions(ctx)[sessionID]; ok {
		return resumedSession{terminalRef: terminal, AlreadyOpen: true}, nil
	}
	if strings.TrimSpace(entry.ResumeCommand) == "" {
		return resumedSession{}, errSessionNotResumable
	}
	created, err := s.cmux.CreateWorkspace(ctx, "", entry.Cwd)
	s.workspaces.refresh()
	if err != nil {
		return resumedSession{}, err
	}
	terminal := terminalRef{WorkspaceID: created.WorkspaceID, SurfaceID: created.SurfaceID}
	if err := s.runWhenReady(ctx, terminal, resumeCommand(entry)); err != nil {
		return resumedSession{}, err
	}
	return resumedSession{terminalRef: terminal}, nil
}

func (s *Server) runWhenReady(ctx context.Context, terminal terminalRef, command string) error {
	if err := s.waitUntilReady(ctx, terminal); err != nil {
		return err
	}
	submitted, err := s.cmux.RunCommand(ctx, terminal.WorkspaceID, terminal.SurfaceID, command)
	if err == nil && !submitted {
		err = s.cmux.SendKey(ctx, terminal.WorkspaceID, terminal.SurfaceID, "enter")
	}
	return err
}

func (s *Server) waitUntilReady(ctx context.Context, terminal terminalRef) error {
	deadline := time.Now().Add(terminalReadyTimeout)
	for {
		list, err := s.cmux.Workspaces(ctx)
		if err == nil && terminalReady(list, terminal) {
			return nil
		}
		_, _ = s.cmux.Replay(ctx, terminal.WorkspaceID, terminal.SurfaceID)
		if time.Now().After(deadline) {
			return &requestError{Code: "not_ready", Message: "The new terminal didn't start in time. Open it and run the session from there."}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(terminalReadyPoll):
		}
	}
}

func terminalReady(list cmux.WorkspaceList, terminal terminalRef) bool {
	for _, workspace := range list.Workspaces {
		if workspace.ID != terminal.WorkspaceID {
			continue
		}
		for _, candidate := range workspace.Terminals {
			if candidate.ID == terminal.SurfaceID {
				return candidate.IsReady
			}
		}
	}
	return false
}

func resumeCommand(entry cmux.VaultSession) string {
	if entry.Agent != agentKindClaude || os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return entry.ResumeCommand
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return entry.ResumeCommand
	}
	defaultDir := "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude")
	command := strings.ReplaceAll(entry.ResumeCommand, " "+defaultDir+" ", " ")
	return strings.ReplaceAll(command, " '"+defaultDir+"' ", " ")
}
