package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const (
	organizingConcurrency = 4
	maxChecklistTextBytes = 1024
	maxDescriptionBytes   = 1024
	maxGroupNameBytes     = 256
	newWorkspaceWait      = 3 * time.Second
)

var workspaceActions = map[string]bool{
	"pin": true, "unpin": true,
	"mark_read": true, "mark_unread": true,
	"set_color": true, "clear_color": true,
	"set_description": true, "clear_description": true,
}

var lanes = map[string]bool{"todo": true, "working": true, "needs-attention": true, "review": true, "done": true, "auto": true}

var checklistStates = map[string]bool{"pending": true, "in-progress": true, "completed": true}

var errOrganizingUnsupported = unsupported("do that")

func (s *Server) pollWorkspaces(git *gitInfo) func(ctx context.Context) ([]byte, bool) {
	return func(ctx context.Context) ([]byte, bool) {
		list, err := s.cmux.Workspaces(ctx)
		if err != nil {
			s.logger.Warn("could not list workspaces", "error", err)
			return nil, false
		}
		git.refresh(ctx, s)
		git.discover(ctx, s, list.Workspaces)
		git.apply(list.Workspaces)
		s.organizing.refresh(ctx, s, list)
		s.organizing.apply(&list)
		encoded, err := json.Marshal(map[string]any{"type": "workspaces", "workspaces": list.Workspaces, "groups": list.Groups})
		return encoded, err == nil
	}
}

type workspaceExtras struct {
	mu                   sync.Mutex
	fetchedAt            time.Time
	invalidations        uint64
	fetchedInvalidations uint64
	status               map[string]cmux.LaneStatus
	checklists           map[string]cmux.ChecklistProgress
	remotes              map[string]cmux.RemoteState
	groupColors          map[string]string
}

func (e *workspaceExtras) invalidate() {
	e.mu.Lock()
	e.invalidations++
	e.mu.Unlock()
}

func (e *workspaceExtras) due(interval time.Duration) (bool, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.invalidations != e.fetchedInvalidations || time.Since(e.fetchedAt) >= interval, e.invalidations
}

func (e *workspaceExtras) refresh(ctx context.Context, s *Server, list cmux.WorkspaceList) {
	due, invalidations := e.due(s.options.PollInterval.Organizing)
	if !due {
		return
	}
	status := map[string]cmux.LaneStatus{}
	checklists := map[string]cmux.ChecklistProgress{}
	remotes := map[string]cmux.RemoteState{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, organizingConcurrency)
	for _, workspace := range list.Workspaces {
		id := workspace.ID
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			lane, laneOK := s.readLane(ctx, id)
			progress, progressOK := s.readChecklistProgress(ctx, id)
			remote := s.readRemote(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if laneOK {
				status[id] = lane
			}
			if progressOK && progress.Total > 0 {
				checklists[id] = progress
			}
			if remote != nil {
				remotes[id] = *remote
			}
		})
	}
	wg.Wait()
	groupColors := s.readGroupColors(ctx, list)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fetchedAt = time.Now()
	e.fetchedInvalidations = invalidations
	e.status, e.checklists, e.remotes, e.groupColors = status, checklists, remotes, groupColors
}

func (e *workspaceExtras) apply(list *cmux.WorkspaceList) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for index := range list.Workspaces {
		workspace := &list.Workspaces[index]
		if lane, ok := e.status[workspace.ID]; ok {
			workspace.Status = &lane
		}
		if progress, ok := e.checklists[workspace.ID]; ok {
			workspace.Checklist = &progress
		}
		if remote, ok := e.remotes[workspace.ID]; ok {
			workspace.Remote = &remote
		}
	}
	for index := range list.Groups {
		group := &list.Groups[index]
		if color, ok := e.groupColors[group.ID]; ok {
			group.CustomColor = &color
		}
	}
}

func (s *Server) readLane(ctx context.Context, workspaceID string) (cmux.LaneStatus, bool) {
	lane, ok := probe(s, cmux.MethodLaneStatus, func() (cmux.LaneStatus, error) { return s.cmux.LaneStatus(ctx, workspaceID) })
	return lane, ok && lane.Effective != ""
}

func (s *Server) readChecklistProgress(ctx context.Context, workspaceID string) (cmux.ChecklistProgress, bool) {
	checklist, ok := probe(s, cmux.MethodChecklist, func() (cmux.Checklist, error) { return s.cmux.Checklist(ctx, workspaceID) })
	return checklist.Progress, ok
}

func (s *Server) readRemote(ctx context.Context, workspaceID string) *cmux.RemoteState {
	remote, _ := probe(s, cmux.MethodRemoteStatus, func() (*cmux.RemoteState, error) { return s.cmux.RemoteState(ctx, workspaceID) })
	return remote
}

func (s *Server) readGroupColors(ctx context.Context, list cmux.WorkspaceList) map[string]string {
	if len(list.Groups) == 0 {
		return nil
	}
	groups, ok := probe(s, cmux.MethodGroupList, func() ([]cmux.GroupDetails, error) { return s.cmux.GroupDetails(ctx) })
	if !ok {
		return nil
	}
	colors := map[string]string{}
	for _, group := range groups {
		if group.CustomColor != nil && *group.CustomColor != "" {
			colors[group.ID] = *group.CustomColor
		}
	}
	return colors
}

func (s *Server) organize(ctx context.Context, method string, params map[string]any, result any) error {
	err := guard(s, method, errOrganizingUnsupported, func() error { return s.cmux.Call(ctx, method, params, result) })
	if !errors.Is(err, errOrganizingUnsupported) {
		s.organizing.invalidate()
		s.workspaces.refresh()
	}
	return err
}

type organizingRequest struct {
	WorkspaceID  string   `json:"workspace_id"`
	GroupID      string   `json:"group_id"`
	ItemID       string   `json:"item_id"`
	Action       string   `json:"action"`
	Status       string   `json:"status"`
	State        string   `json:"state"`
	Text         string   `json:"text"`
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	Color        *string  `json:"color"`
	Description  string   `json:"description"`
	Pinned       bool     `json:"pinned"`
	WorkspaceIDs []string `json:"workspace_ids"`
}

func (s *session) organizingRequest(ctx context.Context, kind string, raw json.RawMessage) (any, error) {
	var request organizingRequest
	if _, known := requestMethods[kind]; !known {
		return nil, unknownType(kind)
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, invalid(kind + " has malformed parameters.")
	}
	server := s.server
	switch kind {
	case "workspace_action":
		if request.WorkspaceID == "" || !workspaceActions[request.Action] {
			return nil, invalid("workspace_action needs workspace_id and one of pin, unpin, mark_read, mark_unread, set_color, clear_color, set_description or clear_description.")
		}
		params := map[string]any{"workspace_id": request.WorkspaceID, "action": request.Action}
		switch request.Action {
		case "set_color":
			if request.Color == nil || strings.TrimSpace(*request.Color) == "" {
				return nil, invalid("set_color needs a color.")
			}
			params["color"] = strings.TrimSpace(*request.Color)
		case "set_description":
			description := strings.TrimSpace(request.Description)
			if description == "" {
				params["action"] = "clear_description"
			} else if len(description) > maxDescriptionBytes {
				return nil, invalid("A description can be up to 1024 bytes.")
			} else {
				params["description"] = description
			}
		}
		return nil, server.organize(ctx, cmux.MethodWorkspaceAction, params, nil)
	case "set_workspace_status":
		if request.WorkspaceID == "" || !lanes[request.Status] {
			return nil, invalid("set_workspace_status needs workspace_id and a status of todo, working, needs-attention, review, done or auto.")
		}
		return nil, server.organize(ctx, cmux.MethodSetLaneStatus, map[string]any{"workspace_id": request.WorkspaceID, "status": request.Status}, nil)
	case "checklist":
		if request.WorkspaceID == "" {
			return nil, invalid("checklist needs workspace_id.")
		}
		checklist, err := server.checklist(ctx, request.WorkspaceID)
		return checklist, err
	case "add_checklist_item", "edit_checklist_item":
		text := strings.TrimSpace(request.Text)
		if request.WorkspaceID == "" || text == "" || (kind == "edit_checklist_item" && request.ItemID == "") {
			return nil, invalid(kind + " needs workspace_id, text and, to edit, item_id.")
		}
		if len(text) > maxChecklistTextBytes {
			return nil, invalid("A checklist item can be up to 1024 bytes.")
		}
		method, params := cmux.MethodChecklistAdd, map[string]any{"workspace_id": request.WorkspaceID, "text": text}
		if kind == "edit_checklist_item" {
			method, params["id"] = cmux.MethodChecklistEdit, request.ItemID
		}
		return server.checklistAfter(ctx, request.WorkspaceID, method, params)
	case "set_checklist_item_state":
		if request.WorkspaceID == "" || request.ItemID == "" || !checklistStates[request.State] {
			return nil, invalid("set_checklist_item_state needs workspace_id, item_id and a state of pending, in-progress or completed.")
		}
		return server.checklistAfter(ctx, request.WorkspaceID, cmux.MethodChecklistState, map[string]any{"workspace_id": request.WorkspaceID, "id": request.ItemID, "state": request.State})
	case "remove_checklist_item":
		if request.WorkspaceID == "" || request.ItemID == "" {
			return nil, invalid("remove_checklist_item needs workspace_id and item_id.")
		}
		return server.checklistAfter(ctx, request.WorkspaceID, cmux.MethodChecklistRemove, map[string]any{"workspace_id": request.WorkspaceID, "id": request.ItemID})
	case "clear_checklist":
		if request.WorkspaceID == "" {
			return nil, invalid("clear_checklist needs workspace_id.")
		}
		return server.checklistAfter(ctx, request.WorkspaceID, cmux.MethodChecklistClear, map[string]any{"workspace_id": request.WorkspaceID})
	case "create_group":
		name := strings.TrimSpace(request.Name)
		if name == "" || len(name) > maxGroupNameBytes || len(request.WorkspaceIDs) == 0 {
			return nil, invalid("create_group needs a name of up to 256 bytes and at least one workspace.")
		}
		var created struct {
			Group struct {
				ID string `json:"id"`
			} `json:"group"`
		}
		err := server.organize(ctx, cmux.MethodGroupCreate, map[string]any{"name": name, "child_workspace_ids": request.WorkspaceIDs}, &created)
		return map[string]string{"group_id": created.Group.ID}, err
	case "rename_group":
		name := strings.TrimSpace(request.Name)
		if request.GroupID == "" || name == "" || len(name) > maxGroupNameBytes {
			return nil, invalid("rename_group needs group_id and a name of up to 256 bytes.")
		}
		return nil, server.organize(ctx, cmux.MethodGroupRename, map[string]any{"group_id": request.GroupID, "name": name}, nil)
	case "pin_group":
		if request.GroupID == "" {
			return nil, invalid("pin_group needs group_id.")
		}
		method := cmux.MethodGroupUnpin
		if request.Pinned {
			method = cmux.MethodGroupPin
		}
		return nil, server.organize(ctx, method, map[string]any{"group_id": request.GroupID}, nil)
	case "set_group_color":
		if request.GroupID == "" {
			return nil, invalid("set_group_color needs group_id.")
		}
		var color any
		if request.Color != nil && strings.TrimSpace(*request.Color) != "" {
			color = strings.TrimSpace(*request.Color)
		}
		return nil, server.organize(ctx, cmux.MethodGroupSetColor, map[string]any{"group_id": request.GroupID, "color": color}, nil)
	case "add_to_group", "remove_from_group":
		if request.GroupID == "" || request.WorkspaceID == "" {
			return nil, invalid(kind + " needs group_id and workspace_id.")
		}
		method := cmux.MethodGroupAdd
		if kind == "remove_from_group" {
			method = cmux.MethodGroupRemove
		}
		return nil, server.organize(ctx, method, map[string]any{"group_id": request.GroupID, "workspace_id": request.WorkspaceID}, nil)
	case "ungroup":
		if request.GroupID == "" {
			return nil, invalid("ungroup needs group_id.")
		}
		return nil, server.organize(ctx, cmux.MethodGroupUngroup, map[string]any{"group_id": request.GroupID}, nil)
	case "create_workspace_in_group":
		if request.GroupID == "" {
			return nil, invalid("create_workspace_in_group needs group_id.")
		}
		created, err := server.createWorkspaceInGroup(ctx, request.GroupID, strings.TrimSpace(request.Title))
		return created, err
	case "reconnect_workspace":
		if request.WorkspaceID == "" {
			return nil, invalid("reconnect_workspace needs workspace_id.")
		}
		return nil, server.organize(ctx, cmux.MethodRemoteReconnect, map[string]any{"workspace_id": request.WorkspaceID}, nil)
	}
	return nil, unknownType(kind)
}

func (s *Server) checklist(ctx context.Context, workspaceID string) (cmux.Checklist, error) {
	return guarded(s, cmux.MethodChecklist, errOrganizingUnsupported, func() (cmux.Checklist, error) {
		return s.cmux.Checklist(ctx, workspaceID)
	})
}

func (s *Server) checklistAfter(ctx context.Context, workspaceID, method string, params map[string]any) (any, error) {
	if err := s.organize(ctx, method, params, nil); err != nil {
		return nil, err
	}
	return s.checklist(ctx, workspaceID)
}

func (s *Server) createWorkspaceInGroup(ctx context.Context, groupID, title string) (cmux.CreatedWorkspace, error) {
	var created cmux.CreatedWorkspace
	if err := s.organize(ctx, cmux.MethodGroupNewSpace, map[string]any{"group_id": groupID}, &created); err != nil {
		return created, err
	}
	if title != "" {
		if err := s.cmux.RenameWorkspace(ctx, created.WorkspaceID, title); err != nil {
			return created, err
		}
	}
	deadline := time.Now().Add(newWorkspaceWait)
	for created.SurfaceID == "" && time.Now().Before(deadline) {
		if list, err := s.cmux.Workspaces(ctx); err == nil {
			for _, workspace := range list.Workspaces {
				if workspace.ID == created.WorkspaceID && len(workspace.Terminals) > 0 {
					created.SurfaceID = workspace.Terminals[0].ID
				}
			}
		}
		if created.SurfaceID == "" {
			select {
			case <-ctx.Done():
				return created, ctx.Err()
			case <-time.After(terminalReadyPoll):
			}
		}
	}
	return created, nil
}
