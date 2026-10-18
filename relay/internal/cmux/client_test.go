package cmux

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

type fakeSocket struct {
	path    string
	mu      sync.Mutex
	methods []string
}

func startFakeSocket(t *testing.T, respond func(method string, params map[string]any, authenticated bool) string) *fakeSocket {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "muxer-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fake := &fakeSocket{path: filepath.Join(dir, "cmux.sock")}
	listener, err := net.Listen("unix", fake.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				authenticated := false
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					var request struct {
						ID     string         `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					}
					_ = json.Unmarshal(scanner.Bytes(), &request)
					fake.mu.Lock()
					fake.methods = append(fake.methods, request.Method)
					fake.mu.Unlock()
					if request.Method == "auth.login" && request.Params["password"] == "secret" {
						authenticated = true
					}
					reply := respond(request.Method, request.Params, authenticated)
					if reply[0] == '{' {
						var decoded map[string]any
						_ = json.Unmarshal([]byte(reply), &decoded)
						decoded["id"] = request.ID
						encoded, _ := json.Marshal(decoded)
						reply = string(encoded)
					}
					_, _ = conn.Write([]byte(reply + "\n"))
				}
			}()
		}
	}()
	return fake
}

func (f *fakeSocket) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func TestClientLogsInWithPasswordBeforeCalling(t *testing.T) {
	fake := startFakeSocket(t, func(method string, _ map[string]any, authenticated bool) string {
		switch {
		case method == "auth.login" && authenticated:
			return `{"ok":true,"result":{"authenticated":true}}`
		case method == "auth.login":
			return `{"ok":false,"error":{"code":"auth_failed","message":"bad"}}`
		case !authenticated:
			return `{"ok":false,"error":{"code":"auth_required","message":"login first"}}`
		default:
			return `{"ok":true,"result":{"version":"0.65.1"}}`
		}
	})

	client := NewClient(fake.path, func() (string, error) { return "secret", nil })
	defer client.Close()
	identity, err := client.Identify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if identity.Version != "0.65.1" {
		t.Fatalf("version = %q", identity.Version)
	}

	wrong := NewClient(fake.path, func() (string, error) { return "nope", nil })
	defer wrong.Close()
	if _, err := wrong.Identify(context.Background()); err == nil {
		t.Fatal("a wrong password must fail")
	}
}

func TestClientToleratesUnconfiguredPasswordAndReportsAccessDenied(t *testing.T) {
	fake := startFakeSocket(t, func(method string, _ map[string]any, _ bool) string {
		if method == "auth.login" {
			return `{"ok":false,"error":{"code":"auth_unconfigured","message":"not password mode"}}`
		}
		return `{"ok":true,"result":{"workspaces":[{"id":"w1","title":"api","group_id":"g1","terminals":[{"id":"t1","title":"zsh"}]}],"groups":[{"id":"g1","name":"Q4","anchor_workspace_id":"w1","member_workspace_ids":["w1"]}]}}`
	})
	client := NewClient(fake.path, func() (string, error) { return "stale", nil })
	defer client.Close()
	list, err := client.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Workspaces) != 1 || list.Workspaces[0].Terminals[0].ID != "t1" {
		t.Fatalf("workspaces = %+v", list.Workspaces)
	}
	if len(list.Groups) != 1 || *list.Groups[0].AnchorWorkspaceID != "w1" || list.Groups[0].MemberWorkspaceIDs[0] != "w1" {
		t.Fatalf("groups = %+v", list.Groups)
	}

	denied := startFakeSocket(t, func(string, map[string]any, bool) string {
		return "ERROR: Access denied - only processes started inside cmux can connect"
	})
	deniedClient := NewClient(denied.path, nil)
	defer deniedClient.Close()
	if _, err := deniedClient.Identify(context.Background()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("want ErrAccessDenied, got %v", err)
	}
	streamErr := deniedClient.Events(context.Background(), nil, EventFilter{Categories: []string{"notification"}}, func(Event) error { return nil })
	if !errors.Is(streamErr, ErrAccessDenied) {
		t.Fatalf("a refused event stream must report ErrAccessDenied, got %v", streamErr)
	}
}

func TestClientRecoversFromConnectionsCmuxClosedWhileIdle(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "muxer-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "cmux.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var mu sync.Mutex
	connections, inputs := 0, 0
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections++
			mu.Unlock()
			go func() {
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var request struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				_ = json.Unmarshal(scanner.Bytes(), &request)
				if request.Method == "mobile.terminal.input" {
					mu.Lock()
					inputs++
					mu.Unlock()
				}
				_, _ = conn.Write([]byte(`{"id":"` + request.ID + `","ok":true,"result":{"version":"1"}}` + "\n"))
			}()
		}
	}()

	client := NewClient(path, nil)
	defer client.Close()
	for range 3 {
		if _, err := client.Identify(context.Background()); err != nil {
			t.Fatalf("a read must survive a connection cmux closed while it sat idle: %v", err)
		}
	}
	_ = client.Input(context.Background(), "w", "s", "ls\r")
	mu.Lock()
	defer mu.Unlock()
	if inputs > 1 {
		t.Fatalf("input must never be sent twice, got %d", inputs)
	}
	if connections < 3 {
		t.Fatalf("expected fresh connections after each close, got %d", connections)
	}
}

func TestClientRetriesRateLimitedCalls(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	fake := startFakeSocket(t, func(method string, _ map[string]any, _ bool) string {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 1 {
			return `{"ok":false,"error":{"code":"rate_limited","message":"slow down","data":{"retry_after_ms":5}}}`
		}
		return `{"ok":true,"result":{"version":"1"}}`
	})
	client := NewClient(fake.path, nil)
	defer client.Close()
	if _, err := client.Identify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.calls(); len(got) != 2 {
		t.Fatalf("expected one retry, calls = %v", got)
	}
}

func TestClientSkipsSocketsLeftBehindByACrashedCmux(t *testing.T) {
	live := startFakeSocket(t, func(string, map[string]any, bool) string {
		return `{"ok":true,"result":{"version":"0.65.1"}}`
	})
	orphan := filepath.Join(filepath.Dir(live.path), "cmux-501.sock")
	listener, err := net.Listen("unix", orphan)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()

	client := &Client{socketPaths: func() []string { return []string{orphan, live.path} }}
	defer client.Close()
	if _, err := client.Identify(context.Background()); err != nil {
		t.Fatalf("the client must move on to the socket cmux is listening on: %v", err)
	}
	if client.SocketPath() != live.path {
		t.Fatalf("socket path = %q, want %q", client.SocketPath(), live.path)
	}
}

func TestDefaultSocketPathsTryWhatCmuxLastRecordedFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CMUX_SOCKET_PATH", "")
	state := filepath.Join(home, ".local", "state", "cmux")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(state, "cmux.sock")
	userScoped := filepath.Join(state, fmt.Sprintf("cmux-%d.sock", os.Getuid()))
	if err := os.WriteFile(filepath.Join(state, "last-socket-path"), []byte(userScoped+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := DefaultSocketPaths()
	if !slices.Contains(paths, stable) || paths[0] != userScoped || slices.Index(paths, stable) < 1 {
		t.Fatalf("the recorded path must come first and the stable path still be tried: %v", paths)
	}
	if len(paths) != len(slices.Compact(slices.Clone(paths))) {
		t.Fatalf("paths must not repeat: %v", paths)
	}

	t.Setenv("CMUX_SOCKET_PATH", "/tmp/override.sock")
	if paths := DefaultSocketPaths(); !slices.Equal(paths, []string{"/tmp/override.sock"}) {
		t.Fatalf("CMUX_SOCKET_PATH must win: %v", paths)
	}
}
