package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gantoine/muxer/phcloud/internal/posthogcloud"
)

func TestDockConfigKeepsOtherControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmux", "dock.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"controls":[{"id":"git","command":"lazygit"}],"other":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := updateDockConfig(path, "/bin/phcloud", true); err != nil {
			t.Fatal(err)
		}
	}
	controls := readControls(t, path)
	if len(controls) != 2 || controls[0]["id"] != "git" || controls[1]["id"] != dockControlID || controls[1]["command"] != "'/bin/phcloud'" {
		t.Fatalf("controls = %v", controls)
	}
	if changed, err := updateDockConfig(path, "/bin/phcloud", false); err != nil || !changed {
		t.Fatalf("uninstall = %v %v", changed, err)
	}
	if controls := readControls(t, path); len(controls) != 1 || controls[0]["id"] != "git" {
		t.Fatalf("controls after uninstall = %v", controls)
	}
	if changed, _ := updateDockConfig(path, "/bin/phcloud", false); changed {
		t.Fatal("a second uninstall shouldn't touch the file")
	}
}

func readControls(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Controls []map[string]any `json:"controls"`
		Other    bool             `json:"other"`
	}
	if err := json.Unmarshal(raw, &config); err != nil || !config.Other {
		t.Fatalf("config = %s (%v)", raw, err)
	}
	return config.Controls
}

func TestLanesFollowTheRun(t *testing.T) {
	cases := []struct{ status, state, lane string }{
		{"in_progress", posthogcloud.StateWorking, "working"},
		{"in_progress", posthogcloud.StateWaiting, "needs-attention"},
		{"in_progress", posthogcloud.StateIdle, "review"},
		{"queued", "", "working"},
		{"completed", posthogcloud.StateIdle, "done"},
		{"failed", "", "needs-attention"},
	}
	for _, c := range cases {
		if lane := laneFor(c.status, c.state); lane != c.lane {
			t.Errorf("laneFor(%s, %s) = %s, want %s", c.status, c.state, lane, c.lane)
		}
	}
}

func TestRunsGroupByRepositoryName(t *testing.T) {
	for repository, group := range map[string]string{"acme/api": "api", "": ungroupedName, "solo": "solo"} {
		if got := repositoryGroup(repository); got != group {
			t.Errorf("repositoryGroup(%q) = %q, want %q", repository, got, group)
		}
	}
}

func TestNotificationsOnlyForStatesThatNeedYou(t *testing.T) {
	task := posthogcloud.Task{Title: "Fix login"}
	waiting := liveState{status: "in_progress", state: posthogcloud.StateWaiting, pending: []posthogcloud.PendingApproval{{Kind: posthogcloud.ApprovalPermission, Title: "Run tests"}}}
	if title, body := notification(task, waiting); title != "Cloud run needs you" || body != "Fix login: Run tests" {
		t.Errorf("waiting = %q %q", title, body)
	}
	if title, _ := notification(task, liveState{status: "in_progress", state: posthogcloud.StateWorking}); title != "" {
		t.Errorf("working shouldn't notify, got %q", title)
	}
	if title, _ := notification(task, liveState{status: "completed"}); title != "Cloud run finished" {
		t.Errorf("completed = %q", title)
	}
}

func TestStartedTaskFallsBackToTheRequest(t *testing.T) {
	task := startedTask(posthogcloud.Task{ID: "t1"}, posthogcloud.Run{ID: "r1"}, posthogcloud.StartRequest{Repository: posthogcloud.Repository{Name: "acme/api"}, Prompt: "Fix the flaky test\nmore detail"})
	if taskTitle(task) != "Fix the flaky test" || task.Repository != "acme/api" || task.LatestRun.ID != "r1" || task.LatestRun.Status != "queued" {
		t.Fatalf("task = %+v", task)
	}
}

func TestViewerCommands(t *testing.T) {
	for line, want := range map[string]viewerAction{
		"/stop":    {kind: actionStop},
		"/approve": {kind: actionAnswer, mode: answerOnce},
		"/deny":    {kind: actionAnswer, mode: answerDeny},
		"go on":    {kind: actionSend, text: "go on"},
	} {
		if got, err := parseViewerLine(line); err != nil || got != want {
			t.Errorf("parseViewerLine(%q) = %+v %v", line, got, err)
		}
	}
	if _, err := parseViewerLine("/nope"); err == nil {
		t.Error("an unknown command should fail")
	}
}
