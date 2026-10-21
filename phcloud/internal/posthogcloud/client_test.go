package posthogcloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestClientListsTheOwnersTasks(t *testing.T) {
	var seen *http.Request
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"t1","task_number":7,"title":"Fix login","repository":"acme/app","latest_run":{"id":"r1","status":"in_progress"}}]}`))
	}))
	defer api.Close()

	client := NewClient(Config{Host: api.URL + "/", ProjectID: 2, APIKey: "phx_key", UserID: 42}, api.Client())
	tasks, err := client.Tasks(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Number != 7 || tasks[0].LatestRun.ID != "r1" {
		t.Fatalf("tasks = %+v", tasks)
	}
	if seen.URL.Path != "/api/projects/2/tasks/" || seen.Header.Get("Authorization") != "Bearer phx_key" {
		t.Fatalf("request = %s %v", seen.URL.Path, seen.Header)
	}
	query := seen.URL.Query()
	if query.Get("created_by") != "42" || query.Get("ordering") != "-last_activity_at" || query.Get("exclude_origin_product") != "signals_scout" {
		t.Fatalf("query = %v", query)
	}
}

func TestClientPagesTheRunLog(t *testing.T) {
	var offsets []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/2/tasks/t1/runs/r1/session_logs/" {
			http.NotFound(w, r)
			return
		}
		offsets = append(offsets, r.URL.Query().Get("offset"))
		if r.URL.Query().Get("exclude_types") == "" {
			t.Error("console lines must be filtered on the server")
		}
		w.Header().Set("X-Has-More", "true")
		_, _ = w.Write([]byte(`[{"type":"notification"},{"type":"notification"}]`))
	}))
	defer api.Close()

	client := NewClient(Config{Host: api.URL, ProjectID: 2, APIKey: "k"}, api.Client())
	entries, more, err := client.SessionLogs(context.Background(), "t1", "r1", 10)
	if err != nil || len(entries) != 2 || !more || offsets[0] != "10" {
		t.Fatalf("entries=%d more=%v err=%v offsets=%v", len(entries), more, err, offsets)
	}
}

func TestClientNamesAuthFailures(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer api.Close()
	_, err := NewClient(Config{Host: api.URL, ProjectID: 2, APIKey: "bad"}, api.Client()).Me(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigRoundTripsPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "posthog-cloud.json")
	if _, ok, err := LoadConfig(path); ok || err != nil {
		t.Fatalf("a missing file must read as not connected: %v %v", ok, err)
	}
	if err := SaveConfig(path, Config{ProjectID: 2, APIKey: "k", UserID: 1}); err != nil {
		t.Fatal(err)
	}
	config, ok, err := LoadConfig(path)
	if err != nil || !ok || config.Host != DefaultHost || config.APIKey != "k" {
		t.Fatalf("config = %+v ok=%v err=%v", config, ok, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key file must be private: %v %v", info, err)
	}
	if err := RemoveConfig(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveConfig(path); err != nil {
		t.Fatalf("removing twice must be fine: %v", err)
	}
}
