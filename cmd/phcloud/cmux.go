package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gantoine/phcloud/internal/posthogcloud"
)

const ungroupedName = "PostHog Cloud"

type cmux struct {
	bin  string
	self string

	mu         sync.Mutex
	path       string
	workspaces map[string]string
	lanes      map[string]string
}

func newCmux(statePath string) *cmux {
	bin, err := exec.LookPath("cmux")
	if err != nil {
		bin = "/Applications/cmux.app/Contents/Resources/bin/cmux"
	}
	self, _ := os.Executable()
	c := &cmux{bin: bin, self: cmp.Or(self, "phcloud"), path: statePath, workspaces: map[string]string{}, lanes: map[string]string{}}
	if raw, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(raw, &c.workspaces)
	}
	return c
}

func (c *cmux) rpc(ctx context.Context, method string, params any, into any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, c.bin, "rpc", method, string(encoded))
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("cmux %s: %s", method, cmp.Or(strings.TrimSpace(stderr.String()), err.Error()))
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(stdout.Bytes(), into)
}

func (c *cmux) run(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, c.bin, args...)
	command.Env = append(os.Environ(), "CMUX_QUIET=1")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmux %s: %s", args[0], cmp.Or(strings.TrimSpace(string(output)), err.Error()))
	}
	return nil
}

func (c *cmux) liveWorkspaces(ctx context.Context) (map[string]bool, error) {
	var list struct {
		Workspaces []struct {
			ID string `json:"id"`
		} `json:"workspaces"`
	}
	if err := c.rpc(ctx, "workspace.list", map[string]any{}, &list); err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, workspace := range list.Workspaces {
		live[workspace.ID] = true
	}
	return live, nil
}

func (c *cmux) workspaceFor(ctx context.Context, runID string) string {
	c.mu.Lock()
	workspaceID := c.workspaces[runID]
	c.mu.Unlock()
	if workspaceID == "" {
		return ""
	}
	live, err := c.liveWorkspaces(ctx)
	if err != nil || live[workspaceID] {
		return workspaceID
	}
	c.forget(runID)
	return ""
}

func (c *cmux) forget(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.workspaces, runID)
	delete(c.lanes, runID)
	c.saveLocked()
}

func (c *cmux) saveLocked() {
	raw, err := json.MarshalIndent(c.workspaces, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err == nil {
		_ = os.WriteFile(c.path, append(raw, '\n'), 0o600)
	}
}

func (c *cmux) open(ctx context.Context, task posthogcloud.Task) error {
	if task.LatestRun == nil {
		return errors.New("this task has no run yet")
	}
	runID := task.LatestRun.ID
	if workspaceID := c.workspaceFor(ctx, runID); workspaceID != "" {
		return c.rpc(ctx, "workspace.select", map[string]any{"workspace_id": workspaceID}, nil)
	}
	home, _ := os.UserHomeDir()
	command := "clear; " + shellQuote(c.self) + " attach " + shellQuote(task.ID) + " " + shellQuote(runID) + "\r"
	var created struct {
		WorkspaceID string `json:"workspace_id"`
	}
	params := map[string]any{"title": taskTitle(task), "cwd": home, "focus": true, "initial_input": command}
	if dir := os.Getenv("PHCLOUD_CONFIG_DIR"); dir != "" {
		params["workspace_env"] = map[string]string{"PHCLOUD_CONFIG_DIR": dir}
	}
	if err := c.rpc(ctx, "workspace.create", params, &created); err != nil {
		return err
	}
	if created.WorkspaceID == "" {
		return errors.New("cmux didn't say which workspace it opened")
	}
	c.mu.Lock()
	c.workspaces[runID] = created.WorkspaceID
	c.saveLocked()
	c.mu.Unlock()
	if groupID := c.group(ctx, repositoryGroup(task.Repository), home); groupID != "" {
		_ = c.rpc(ctx, "workspace.group.add", map[string]any{"group_id": groupID, "workspace_id": created.WorkspaceID}, nil)
	}
	_ = c.rpc(ctx, "workspace.select", map[string]any{"workspace_id": created.WorkspaceID}, nil)
	return nil
}

func (c *cmux) close(ctx context.Context, runID string) {
	workspaceID := c.workspaceFor(ctx, runID)
	if workspaceID == "" {
		return
	}
	_ = c.rpc(ctx, "workspace.close", map[string]any{"workspace_id": workspaceID, "force": true}, nil)
	c.forget(runID)
}

func (c *cmux) group(ctx context.Context, name, cwd string) string {
	var made struct {
		Group struct {
			ID string `json:"id"`
		} `json:"group"`
	}
	params := map[string]any{"name": name, "idempotency_key": "phcloud:" + name, "cwd": cwd}
	if err := c.rpc(ctx, "workspace.group.create", params, &made); err != nil {
		return ""
	}
	return made.Group.ID
}

func (c *cmux) setLane(ctx context.Context, runID, lane string) {
	c.mu.Lock()
	workspaceID, unchanged := c.workspaces[runID], c.lanes[runID] == lane
	c.mu.Unlock()
	if workspaceID == "" || unchanged {
		return
	}
	if c.run(ctx, "workspace", "status", "set", lane, "--workspace", workspaceID) == nil {
		c.mu.Lock()
		c.lanes[runID] = lane
		c.mu.Unlock()
	}
}

func (c *cmux) notify(ctx context.Context, runID, title, body string) {
	args := []string{"notify", "--title", title, "--body", body}
	c.mu.Lock()
	workspaceID := c.workspaces[runID]
	c.mu.Unlock()
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	_ = c.run(ctx, args...)
}

func repositoryGroup(repository string) string {
	if repository == "" {
		return ungroupedName
	}
	_, name, found := strings.Cut(repository, "/")
	if !found {
		return repository
	}
	return name
}

func laneFor(status, state string) string {
	switch status {
	case "completed", "cancelled":
		return "done"
	case "failed":
		return "needs-attention"
	case "queued", "not_started":
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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
