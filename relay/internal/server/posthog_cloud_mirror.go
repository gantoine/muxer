package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

const (
	postHogCloudMirrorWindow     = 24 * time.Hour
	postHogCloudDismissedMemory  = 48 * time.Hour
	postHogCloudCreatesPerSync   = 4
	postHogCloudUngroupedName    = "PostHog Cloud"
	postHogCloudDescriptionStart = "PostHog Cloud"
)

type mirroredRun struct {
	TaskID      string `json:"task_id"`
	WorkspaceID string `json:"workspace_id"`
	SurfaceID   string `json:"surface_id"`
	Title       string `json:"title"`
	Repository  string `json:"repository,omitempty"`
	Status      string `json:"-"`
	Lane        string `json:"-"`
}

type mirrorFile struct {
	Runs      map[string]*mirroredRun `json:"runs"`
	Dismissed map[string]time.Time    `json:"dismissed"`
	Groups    map[string]string       `json:"groups"`
}

type cloudMirror struct {
	server *Server
	path   string

	mu        sync.Mutex
	loaded    bool
	runs      map[string]*mirroredRun
	dismissed map[string]time.Time
	groups    map[string]string
	starting  map[string]bool
}

func newCloudMirror(server *Server, path string) *cloudMirror {
	return &cloudMirror{server: server, path: path, runs: map[string]*mirroredRun{}, dismissed: map[string]time.Time{}, groups: map[string]string{}, starting: map[string]bool{}}
}

func (m *cloudMirror) enabled() bool {
	return m.path != ""
}

func (m *cloudMirror) loadLocked() {
	if m.loaded || !m.enabled() {
		return
	}
	m.loaded = true
	raw, err := os.ReadFile(m.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var file mirrorFile
	if err != nil || json.Unmarshal(raw, &file) != nil {
		m.server.logger.Warn("could not read the PostHog Cloud workspace list", "path", m.path, "error", err)
		return
	}
	if file.Runs != nil {
		m.runs = file.Runs
	}
	if file.Dismissed != nil {
		m.dismissed = file.Dismissed
	}
	if file.Groups != nil {
		m.groups = file.Groups
	}
}

func (m *cloudMirror) saveLocked() {
	raw, err := json.MarshalIndent(mirrorFile{Runs: m.runs, Dismissed: m.dismissed, Groups: m.groups}, "", "  ")
	if err != nil {
		return
	}
	temporary := m.path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		m.server.logger.Warn("could not save the PostHog Cloud workspace list", "error", err)
		return
	}
	_ = os.Rename(temporary, m.path)
}

func (m *cloudMirror) runForSurface(surfaceID string) (runID string, run mirroredRun, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	for id, mirrored := range m.runs {
		if mirrored.SurfaceID == surfaceID {
			return id, *mirrored, true
		}
	}
	return "", mirroredRun{}, false
}

func (m *cloudMirror) run(runID string) (mirroredRun, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	mirrored, ok := m.runs[runID]
	if !ok {
		return mirroredRun{}, false
	}
	return *mirrored, true
}

func wanted(task posthogcloud.Task, now time.Time) bool {
	run := task.LatestRun
	if run == nil || run.Status == "not_started" {
		return false
	}
	if !posthogcloud.Terminal(run.Status) {
		return true
	}
	finished, err := time.Parse(time.RFC3339Nano, cmp.Or(run.CompletedAt, task.UpdatedAt))
	return err == nil && now.Sub(finished) < postHogCloudMirrorWindow
}

func (m *cloudMirror) sync(ctx context.Context, tasks []posthogcloud.Task) {
	if !m.enabled() || !m.server.methods.supports(cmux.MethodCreateWorkspace) {
		return
	}
	list, err := m.server.cmux.Workspaces(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	workspaces := map[string]cmux.Workspace{}
	for _, workspace := range list.Workspaces {
		workspaces[workspace.ID] = workspace
	}
	desired := map[string]posthogcloud.Task{}
	for _, task := range tasks {
		if wanted(task, now) {
			desired[task.LatestRun.ID] = task
		}
	}

	m.mu.Lock()
	m.loadLocked()
	changed := false
	var closing []string
	for runID, mirrored := range m.runs {
		workspace, open := workspaces[mirrored.WorkspaceID]
		switch {
		case !open:
			delete(m.runs, runID)
			m.dismissed[runID] = now
			changed = true
		case desired[runID].ID == "" && !workspace.IsPinned && workspace.Title == mirrored.Title:
			closing = append(closing, mirrored.WorkspaceID)
			delete(m.runs, runID)
			changed = true
		}
	}
	for runID, at := range m.dismissed {
		if now.Sub(at) > postHogCloudDismissedMemory {
			delete(m.dismissed, runID)
			changed = true
		}
	}
	var creating []posthogcloud.Task
	for runID, task := range desired {
		if mirrored, ok := m.runs[runID]; ok {
			mirrored.Status = task.LatestRun.Status
			continue
		}
		if _, gone := m.dismissed[runID]; gone || m.starting[runID] || len(creating) >= postHogCloudCreatesPerSync {
			continue
		}
		m.starting[runID] = true
		creating = append(creating, task)
	}
	if changed {
		m.saveLocked()
	}
	m.mu.Unlock()

	for _, workspaceID := range closing {
		if err := m.server.cmux.CloseWorkspace(ctx, workspaceID, true); err != nil {
			m.server.logger.Warn("could not close a finished PostHog Cloud workspace", "workspace", workspaceID, "error", err)
		}
	}
	m.removeEmptyGroups(ctx, list, closing)
	slices.SortFunc(creating, func(a, b posthogcloud.Task) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	for _, task := range creating {
		m.create(ctx, task)
	}
	if len(closing) > 0 || len(creating) > 0 {
		m.server.workspaces.refresh()
	}
	m.syncLanes(ctx)
}

func groupName(repository string) string {
	return cmp.Or(strings.TrimSpace(repository), postHogCloudUngroupedName)
}

func (m *cloudMirror) removeEmptyGroups(ctx context.Context, list cmux.WorkspaceList, closed []string) {
	m.mu.Lock()
	owned := map[string]string{}
	for name, groupID := range m.groups {
		owned[groupID] = name
	}
	m.mu.Unlock()
	for _, group := range list.Groups {
		name, ours := owned[group.ID]
		if !ours {
			continue
		}
		remaining := slices.DeleteFunc(slices.Clone(group.MemberWorkspaceIDs), func(id string) bool {
			return slices.Contains(closed, id) || (group.AnchorWorkspaceID != nil && id == *group.AnchorWorkspaceID)
		})
		if len(remaining) > 0 {
			continue
		}
		if err := m.server.organize(ctx, cmux.MethodGroupUngroup, map[string]any{"group_id": group.ID, "remove_generated_anchor": true}, nil); err != nil {
			continue
		}
		m.mu.Lock()
		delete(m.groups, name)
		m.saveLocked()
		m.mu.Unlock()
	}
}

func (m *cloudMirror) group(ctx context.Context, name, cwd string) string {
	m.mu.Lock()
	groupID := m.groups[name]
	m.mu.Unlock()
	var made struct {
		Group struct {
			ID string `json:"id"`
		} `json:"group"`
	}
	params := map[string]any{"name": name, "idempotency_key": "posthog-cloud:" + name, "cwd": cwd}
	if err := m.server.organize(ctx, cmux.MethodGroupCreate, params, &made); err != nil || made.Group.ID == "" {
		return groupID
	}
	m.mu.Lock()
	m.groups[name] = made.Group.ID
	m.mu.Unlock()
	return made.Group.ID
}

func (m *cloudMirror) create(ctx context.Context, task posthogcloud.Task) {
	runID := task.LatestRun.ID
	defer func() {
		m.mu.Lock()
		delete(m.starting, runID)
		m.mu.Unlock()
	}()
	title := cmp.Or(strings.TrimSpace(task.Title), "PostHog Cloud run")
	home, _ := os.UserHomeDir()
	created, err := m.server.cmux.CreateWorkspaceWithEnv(ctx, title, home, m.server.options.PostHogCloudEnv)
	if err != nil {
		m.server.logger.Warn("could not open a workspace for a PostHog Cloud run", "run", runID, "error", err)
		return
	}
	mirrored := &mirroredRun{TaskID: task.ID, WorkspaceID: created.WorkspaceID, SurfaceID: created.SurfaceID, Title: title, Repository: task.Repository, Status: task.LatestRun.Status}
	m.mu.Lock()
	m.runs[runID] = mirrored
	m.saveLocked()
	m.mu.Unlock()

	if groupID := m.group(ctx, groupName(task.Repository), home); groupID != "" {
		_ = m.server.organize(ctx, cmux.MethodGroupAdd, map[string]any{"group_id": groupID, "workspace_id": created.WorkspaceID}, nil)
	}
	description := postHogCloudDescriptionStart
	if task.Repository != "" {
		description += " · " + task.Repository
	}
	_ = m.server.organize(ctx, cmux.MethodWorkspaceAction, map[string]any{"workspace_id": created.WorkspaceID, "action": "set_description", "description": description}, nil)
	command := "clear; " + shellQuote(cmp.Or(m.server.options.PostHogCloudExecutable, "muxer")) + " posthog-cloud attach --task " + shellQuote(task.ID) + " --run " + shellQuote(runID)
	go func() {
		readyCtx, cancel := context.WithTimeout(m.server.ctx, 2*terminalReadyTimeout)
		defer cancel()
		if err := m.server.runWhenReady(readyCtx, terminalRef{WorkspaceID: created.WorkspaceID, SurfaceID: created.SurfaceID}, command); err != nil {
			m.server.logger.Warn("could not start the PostHog Cloud viewer", "run", runID, "error", err)
			return
		}
		_ = m.server.cmux.RenameTerminal(readyCtx, created.WorkspaceID, created.SurfaceID, title)
	}()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func laneFor(status, state string) string {
	switch status {
	case "completed", "cancelled":
		return "done"
	case "failed":
		return "needs-attention"
	case "queued":
		return "working"
	}
	switch state {
	case posthogcloud.StateWaiting:
		return "needs-attention"
	case posthogcloud.StateIdle:
		return "review"
	}
	return "working"
}

func agentStateFor(status, state string) string {
	switch status {
	case "completed":
		return "Done"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Stopped"
	case "queued":
		return "Starting"
	}
	switch state {
	case posthogcloud.StateWaiting:
		return "Needs approval"
	case posthogcloud.StateIdle:
		return "Waiting for your reply"
	}
	return "Running"
}

func (m *cloudMirror) snapshot() map[string]mirroredRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	runs := make(map[string]mirroredRun, len(m.runs))
	for runID, mirrored := range m.runs {
		runs[runID] = *mirrored
	}
	return runs
}

func (m *cloudMirror) current(runID string, mirrored mirroredRun) (status, state string) {
	status, state = m.server.cloud.live(runID)
	return cmp.Or(status, mirrored.Status), state
}

func (m *cloudMirror) syncLanes(ctx context.Context) {
	lanes := map[string]string{}
	for runID, mirrored := range m.snapshot() {
		if status, state := m.current(runID, mirrored); status != "" {
			lanes[runID] = laneFor(status, state)
		}
	}
	type laneChange struct{ workspaceID, lane string }
	var changes []laneChange
	m.mu.Lock()
	for runID, lane := range lanes {
		if mirrored, ok := m.runs[runID]; ok && mirrored.Lane != lane {
			mirrored.Lane = lane
			changes = append(changes, laneChange{mirrored.WorkspaceID, lane})
		}
	}
	m.mu.Unlock()
	for _, change := range changes {
		_ = m.server.organize(ctx, cmux.MethodSetLaneStatus, map[string]any{"workspace_id": change.workspaceID, "status": change.lane}, nil)
	}
	if len(changes) > 0 {
		m.server.workspaces.refresh()
	}
}

func (m *cloudMirror) decorate(list *cmux.WorkspaceList) {
	if !m.enabled() {
		return
	}
	runs := m.snapshot()
	if len(runs) == 0 {
		return
	}
	bySurface := map[string]string{}
	for runID, mirrored := range runs {
		bySurface[mirrored.SurfaceID] = runID
	}
	for w := range list.Workspaces {
		for t := range list.Workspaces[w].Terminals {
			terminal := &list.Workspaces[w].Terminals[t]
			runID, ok := bySurface[terminal.ID]
			if !ok {
				continue
			}
			status, state := m.current(runID, runs[runID])
			agent := cmp.Or(m.server.cloud.agent(runID), agentKindClaude)
			label := agentStateFor(status, state)
			terminal.AgentSource = &agent
			terminal.AgentState = &label
		}
	}
}

func (m *cloudMirror) touched(ctx context.Context, runID string) {
	if _, ok := m.run(runID); ok {
		m.syncLanes(ctx)
	}
}
