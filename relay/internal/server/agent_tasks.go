package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const (
	maxTaskPromptBytes     = 32 << 10
	taskPromptVariable     = "MUXER_TASK_PROMPT"
	taskInWorkspaceFeature = "start_agent_task.workspace_id"
)

var TaskAgents = []string{agentKindClaude, agentKindCodex}

var taskAgentNames = map[string]string{agentKindClaude: "Claude Code", agentKindCodex: "Codex"}

var (
	errAgentTasksUnsupported       = unsupported("start agent tasks")
	errTasksInWorkspaceUnsupported = unsupported("start agent tasks in an open workspace")
	errTaskWorkspaceNotFound       = &requestError{Code: "not_found", Message: "That workspace isn't open on the computer anymore."}
	errTaskWorkspaceRemote         = &requestError{Code: "invalid_params", Message: "Tasks can't start in an SSH workspace. Pick a workspace on the computer itself."}
)

type agentTaskRequest struct {
	Agent       string `json:"agent"`
	Cwd         string `json:"cwd"`
	Prompt      string `json:"prompt"`
	Title       string `json:"title"`
	WorkspaceID string `json:"workspace_id"`
}

func parseAgentTask(raw json.RawMessage) (agentTaskRequest, error) {
	var request agentTaskRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return request, invalid("start_agent_task needs agent, cwd and prompt.")
	}
	if !slices.Contains(TaskAgents, request.Agent) {
		return request, invalid("The agent must be claude or codex.")
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		return request, invalid("Write what the agent should do.")
	}
	if len(request.Prompt) > maxTaskPromptBytes {
		return request, invalid(fmt.Sprintf("A prompt can be up to %d KB.", maxTaskPromptBytes>>10))
	}
	request.Title = strings.TrimSpace(request.Title)
	if len(request.Title) > maxTerminalTitleBytes {
		return request, invalid(fmt.Sprintf("A workspace name can be up to %d bytes.", maxTerminalTitleBytes))
	}
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	if request.WorkspaceID != "" && strings.TrimSpace(request.Cwd) == "" {
		request.Cwd = ""
		return request, nil
	}
	cwd, err := taskFolder(request.Cwd)
	if err != nil {
		return request, err
	}
	request.Cwd = cwd
	return request, nil
}

func taskFolder(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", invalid("Use the folder's full path.")
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return "", invalid("Use the folder's full path, like /Users/you/code/app or ~/code/app.")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", &requestError{Code: "not_found", Message: "There's no folder at " + path + " on the computer."}
	}
	return path, nil
}

func (s *Server) installedAgents(ctx context.Context) []string {
	if s.options.InstalledAgents == nil {
		return slices.Clone(TaskAgents)
	}
	found, err := s.options.InstalledAgents(ctx)
	if err != nil {
		return slices.Clone(TaskAgents)
	}
	return found
}

func (s *Server) startAgentTask(ctx context.Context, request agentTaskRequest) (terminalRef, error) {
	var folder string
	if request.WorkspaceID != "" {
		found, err := s.taskWorkspaceFolder(ctx, request.WorkspaceID)
		if err != nil {
			return terminalRef{}, err
		}
		folder = cmp.Or(request.Cwd, found)
	}
	if !slices.Contains(s.installedAgents(ctx), request.Agent) {
		return terminalRef{}, &requestError{Code: "agent_missing", Message: taskAgentNames[request.Agent] + " isn't installed on the computer."}
	}
	env := map[string]string{taskPromptVariable: request.Prompt}
	var terminal terminalRef
	if request.WorkspaceID != "" {
		surfaceID, err := guarded(s, cmux.MethodCreateSurface, errTasksInWorkspaceUnsupported, func() (string, error) {
			return s.cmux.CreateTerminalWithEnv(ctx, request.WorkspaceID, folder, env)
		})
		s.workspaces.refresh()
		if err != nil {
			return terminalRef{}, err
		}
		terminal = terminalRef{WorkspaceID: request.WorkspaceID, SurfaceID: surfaceID}
	} else {
		created, err := guarded(s, cmux.MethodCreateWorkspace, errAgentTasksUnsupported, func() (cmux.CreatedWorkspace, error) {
			return s.cmux.CreateWorkspaceWithEnv(ctx, request.Title, request.Cwd, env)
		})
		s.workspaces.refresh()
		if err != nil {
			return terminalRef{}, err
		}
		terminal = terminalRef{WorkspaceID: created.WorkspaceID, SurfaceID: created.SurfaceID}
	}
	if err := s.runWhenReady(ctx, terminal, taskCommand(request.Agent)); err != nil {
		return terminalRef{}, err
	}
	return terminal, nil
}

func (s *Server) taskWorkspaceFolder(ctx context.Context, workspaceID string) (string, error) {
	if !s.methods.supports(cmux.MethodCreateSurface) {
		return "", errTasksInWorkspaceUnsupported
	}
	list, err := s.cmux.Workspaces(ctx)
	if err != nil {
		return "", err
	}
	index := slices.IndexFunc(list.Workspaces, func(workspace cmux.Workspace) bool { return workspace.ID == workspaceID })
	if index < 0 {
		return "", errTaskWorkspaceNotFound
	}
	if s.readRemote(ctx, workspaceID) != nil {
		return "", errTaskWorkspaceRemote
	}
	if folder := list.Workspaces[index].CurrentDirectory; folder != nil {
		return *folder, nil
	}
	return "", nil
}

func taskCommand(agent string) string {
	return agent + ` -- "$` + taskPromptVariable + `"`
}
