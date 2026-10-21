package posthogcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type recordedPost struct {
	Path string
	Body map[string]any
}

func startAPI(t *testing.T) (*Client, func() []recordedPost) {
	t.Helper()
	var mu sync.Mutex
	var posts []recordedPost
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			posts = append(posts, recordedPost{Path: r.URL.Path, Body: body})
			mu.Unlock()
		}
		switch r.URL.Path {
		case "/api/environments/2/integrations/":
			_, _ = w.Write([]byte(`{"results":[{"id":7,"kind":"github"},{"id":8,"kind":"slack"}]}`))
		case "/api/environments/2/integrations/7/github_repos/":
			if r.URL.Query().Get("search") != "app" || r.URL.Query().Get("compact") != "true" {
				t.Errorf("repo query = %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"repositories":[{"full_name":"Acme/App"},{"full_name":"acme/app-docs"}]}`))
		case "/api/users/@me/integrations/":
			_, _ = w.Write([]byte(`{"results":[{"id":"u1","kind":"github","installation_id":"555"}]}`))
		case "/api/users/@me/integrations/github/555/repos/":
			_, _ = w.Write([]byte(`{"repositories":[{"full_name":"acme/app"},{"full_name":"me/app-scratch"}]}`))
		case "/api/projects/2/tasks/models/":
			_, _ = w.Write([]byte(`{"models":[{"runtime_adapter":"claude","model":"claude-opus-5-5"},{"runtime_adapter":"claude","model":"claude-sonnet-5-5"},{"runtime_adapter":"codex","model":"gpt-6.2"}]}`))
		case "/api/projects/2/tasks/":
			_, _ = w.Write([]byte(`{"id":"t9","title":"Fix login"}`))
		case "/api/projects/2/tasks/t9/runs/":
			_, _ = w.Write([]byte(`{"id":"r9","status":"queued"}`))
		case "/api/projects/2/tasks/t9/runs/r9/start/":
			_, _ = w.Write([]byte(`{"id":"t9","latest_run":{"id":"r9","status":"queued"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	return NewClient(Config{Host: api.URL, ProjectID: 2, APIKey: "k"}, api.Client()), func() []recordedPost {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedPost(nil), posts...)
	}
}

func TestRepositoriesComeFromTeamAndPersonalIntegrations(t *testing.T) {
	client, _ := startAPI(t)
	repositories, err := client.Repositories(context.Background(), " app ")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repository{{"acme/app", "team:7"}, {"acme/app-docs", "team:7"}, {"me/app-scratch", "user:u1"}}
	if len(repositories) != len(want) {
		t.Fatalf("repositories = %+v", repositories)
	}
	for i := range want {
		if repositories[i] != want[i] {
			t.Errorf("repository %d = %+v, want %+v", i, repositories[i], want[i])
		}
	}
}

func TestStartRunCreatesAnInteractiveCloudRun(t *testing.T) {
	client, posts := startAPI(t)
	task, run, err := client.StartRun(context.Background(), StartRequest{
		Repository: Repository{Name: "me/app-scratch", Source: "user:u1"},
		Agent:      AgentClaude,
		ClaudePlan: true,
		Prompt:     "  fix the login bug ",
	})
	if err != nil || task.ID != "t9" || run.ID != "r9" {
		t.Fatalf("task=%+v run=%+v err=%v", task, run, err)
	}
	sent := posts()
	if len(sent) != 3 {
		t.Fatalf("posts = %+v", sent)
	}
	created := sent[0].Body
	if created["description"] != "fix the login bug" || created["repository"] != "me/app-scratch" || created["github_user_integration"] != "u1" || created["runtime_adapter"] != "claude" || created["branch"] != nil {
		t.Errorf("task body = %v", created)
	}
	runBody := sent[1].Body
	if runBody["environment"] != "cloud" || runBody["mode"] != "interactive" || runBody["model"] != "claude-sonnet-5-5" || runBody["claude_model_access"] != "own-subscription" || runBody["initial_permission_mode"] != "auto" {
		t.Errorf("run body = %v", runBody)
	}
	if sent[2].Path != "/api/projects/2/tasks/t9/runs/r9/start/" || sent[2].Body["pending_user_message"] != "fix the login bug" {
		t.Errorf("start = %+v", sent[2])
	}
}

func TestStartRunPicksTheAgentsModelAndTeamIntegration(t *testing.T) {
	client, posts := startAPI(t)
	if _, _, err := client.StartRun(context.Background(), StartRequest{Repository: Repository{Name: "acme/app", Source: "team:7"}, Agent: AgentCodex, Prompt: "tidy"}); err != nil {
		t.Fatal(err)
	}
	sent := posts()
	if sent[0].Body["github_integration"] != float64(7) {
		t.Errorf("task body = %v", sent[0].Body)
	}
	if sent[1].Body["model"] != "gpt-6.2" || sent[1].Body["claude_model_access"] != nil || sent[1].Body["initial_permission_mode"] != nil {
		t.Errorf("a Codex run takes the first Codex model and no Claude settings: %v", sent[1].Body)
	}
	if _, _, err := client.StartRun(context.Background(), StartRequest{Repository: Repository{Name: "acme/app"}, Agent: "pi", Prompt: "x"}); err == nil {
		t.Error("only Claude and Codex runs start from here")
	}
}
