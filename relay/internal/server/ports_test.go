package server

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/tailnet"
)

const testDNSName = "mac.tailnet.ts.net"

type fakeTailnet struct {
	mu        sync.Mutex
	selfErr   error
	proxies   map[int]string
	tcpOnly   map[int]bool
	listeners map[int][]netip.Addr
	served    []string
	unserved  []int
}

func newFakeTailnet() *fakeTailnet {
	return &fakeTailnet{
		proxies:   map[int]string{60001: "http://127.0.0.1:8765"},
		tcpOnly:   map[int]bool{},
		listeners: map[int][]netip.Addr{},
	}
}

func (f *fakeTailnet) Self(context.Context) (tailnet.Self, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.selfErr != nil {
		return tailnet.Self{}, f.selfErr
	}
	return tailnet.Self{DNSName: testDNSName, IPs: []netip.Addr{netip.MustParseAddr("100.64.0.7")}}, nil
}

func (f *fakeTailnet) ServeConfig(context.Context) (tailnet.ServeConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tcp := map[string]any{}
	web := map[string]any{}
	for port, proxy := range f.proxies {
		tcp[strconv.Itoa(port)] = map[string]any{"HTTPS": true}
		web[testDNSName+":"+strconv.Itoa(port)] = map[string]any{"Handlers": map[string]any{"/": map[string]any{"Proxy": proxy}}}
	}
	for port := range f.tcpOnly {
		tcp[strconv.Itoa(port)] = map[string]any{"TCPForward": "127.0.0.1:22"}
	}
	encoded, _ := json.Marshal(map[string]any{"TCP": tcp, "Web": web})
	return tailnet.ParseServeConfig(encoded)
}

func (f *fakeTailnet) Serve(_ context.Context, port int, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proxies[port] = target
	f.served = append(f.served, strconv.Itoa(port)+" "+target)
	return nil
}

func (f *fakeTailnet) Unserve(_ context.Context, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.proxies, port)
	f.unserved = append(f.unserved, port)
	return nil
}

func (f *fakeTailnet) listenersOn(_ context.Context, port int) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listeners[port], nil
}

func (f *fakeTailnet) listen(port int, addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, addr := range addrs {
		f.listeners[port] = append(f.listeners[port], netip.MustParseAddr(addr))
	}
}

func (f *fakeTailnet) calls() ([]string, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.served...), append([]int(nil), f.unserved...)
}

func (f *fakeTailnet) update(change func(f *fakeTailnet)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func portRelay(t *testing.T, ports ...int) (testRelay, context.Context, *websocket.Conn) {
	t.Helper()
	relay := startRelay(t)
	relay.cmux.mu.Lock()
	relay.cmux.terminalGit = []cmux.TerminalGit{{SurfaceID: "t1", ListeningPorts: ports}}
	relay.cmux.mu.Unlock()
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "hello"})
	resultFor(t, ctx, conn, "hello")
	return relay, ctx, conn
}

func ask(t *testing.T, ctx context.Context, conn *websocket.Conn, id, kind string, port int) map[string]any {
	t.Helper()
	request := map[string]any{"type": kind, "id": id}
	if port != 0 {
		request["port"] = port
	}
	_ = wsjson.Write(ctx, conn, request)
	return resultFor(t, ctx, conn, id)
}

func TestPortListeningOnAllInterfacesOpensDirectly(t *testing.T) {
	relay, ctx, conn := portRelay(t, 3000)
	relay.tailnet.listen(3000, "::")

	result := ask(t, ctx, conn, "1", "port_link", 3000)
	data, _ := result["data"].(map[string]any)
	if result["ok"] != true || data["url"] != "http://mac.tailnet.ts.net:3000" || data["needs_expose"] != false || data["exposed"] != false {
		t.Fatalf("a port open on every interface must be reachable as it is: %v", result)
	}
	result = ask(t, ctx, conn, "2", "expose_port", 3000)
	if served, _ := relay.tailnet.calls(); result["ok"] != true || len(served) != 0 {
		t.Fatalf("a reachable port must not be served again: %v, %v", result, served)
	}
}

func TestLoopbackPortIsExposedAndRemoved(t *testing.T) {
	relay, ctx, conn := portRelay(t, 5173)
	relay.tailnet.listen(5173, "::1", "127.0.0.1")

	result := ask(t, ctx, conn, "1", "port_link", 5173)
	data, _ := result["data"].(map[string]any)
	if data["url"] != "https://mac.tailnet.ts.net:5173" || data["needs_expose"] != true {
		t.Fatalf("a loopback port must ask to be exposed: %v", result)
	}

	result = ask(t, ctx, conn, "2", "expose_port", 5173)
	data, _ = result["data"].(map[string]any)
	if result["ok"] != true || data["exposed"] != true || data["needs_expose"] != false {
		t.Fatalf("exposing must answer with the open link: %v", result)
	}
	if served, _ := relay.tailnet.calls(); len(served) != 1 || served[0] != "5173 http://127.0.0.1:5173" {
		t.Fatalf("the port must be served from its IPv4 loopback listener: %v", served)
	}
	stored, err := os.ReadFile(relay.server.options.ExposedPorts)
	if err != nil || !json.Valid(stored) {
		t.Fatalf("the relay must remember what it exposed: %v %s", err, stored)
	}

	result = ask(t, ctx, conn, "3", "exposed_ports", 0)
	ports := result["data"].(map[string]any)["ports"].([]any)
	if len(ports) != 1 || ports[0].(map[string]any)["url"] != "https://mac.tailnet.ts.net:5173" {
		t.Fatalf("only the port Muxer exposed must be listed, not the relay's own entry: %v", result)
	}

	result = ask(t, ctx, conn, "4", "unexpose_port", 5173)
	if result["ok"] != true || len(result["data"].(map[string]any)["ports"].([]any)) != 0 {
		t.Fatalf("unexposing must answer with the remaining ports: %v", result)
	}
	if _, unserved := relay.tailnet.calls(); len(unserved) != 1 || unserved[0] != 5173 {
		t.Fatalf("unexposing must remove the serve entry: %v", unserved)
	}
	relay.tailnet.update(func(f *fakeTailnet) {
		if _, kept := f.proxies[60001]; !kept {
			t.Error("the relay's own serve entry must stay")
		}
	})
}

func TestOnlyTerminalPortsCanBeOpened(t *testing.T) {
	relay, ctx, conn := portRelay(t, 8765, 60001, 4000, 2222)
	relay.tailnet.listen(4000, "127.0.0.1")
	relay.tailnet.listen(2222, "127.0.0.1")
	relay.tailnet.update(func(f *fakeTailnet) { f.tcpOnly[2222] = true })

	for index, port := range []int{9999, 8765, 60001} {
		result := ask(t, ctx, conn, "deny"+strconv.Itoa(index), "expose_port", port)
		if errorCode(result) != "port_not_allowed" {
			t.Fatalf("port %d must be refused: %v", port, result)
		}
	}
	if result := ask(t, ctx, conn, "conflict", "expose_port", 2222); errorCode(result) != "serve_conflict" {
		t.Fatalf("a port someone else serves must not be replaced: %v", result)
	}
	if result := ask(t, ctx, conn, "foreign", "unexpose_port", 60001); errorCode(result) != "not_exposed" {
		t.Fatalf("serve entries Muxer didn't create must be left alone: %v", result)
	}
	if served, unserved := relay.tailnet.calls(); len(served) != 0 || len(unserved) != 0 {
		t.Fatalf("refused requests must not change tailscale serve: %v %v", served, unserved)
	}
}

func TestPortsExplainWhenTailscaleIsUnavailable(t *testing.T) {
	relay, ctx, conn := portRelay(t, 3000)
	relay.tailnet.listen(3000, "127.0.0.1")
	relay.tailnet.update(func(f *fakeTailnet) { f.selfErr = tailnet.ErrNotInstalled })
	if result := ask(t, ctx, conn, "1", "port_link", 3000); errorCode(result) != "tailscale_missing" {
		t.Fatalf("a missing tailscale must be reported: %v", result)
	}
	relay.tailnet.update(func(f *fakeTailnet) { f.selfErr = tailnet.ErrNotRunning })
	if result := ask(t, ctx, conn, "2", "exposed_ports", 0); errorCode(result) != "tailscale_offline" {
		t.Fatalf("a logged out tailscale must be reported: %v", result)
	}
}

func TestExposedPortsForgetEntriesRemovedOutsideMuxer(t *testing.T) {
	relay, ctx, conn := portRelay(t, 4000)
	relay.tailnet.listen(4000, "127.0.0.1")
	ask(t, ctx, conn, "1", "expose_port", 4000)
	relay.tailnet.update(func(f *fakeTailnet) { f.proxies[4000] = "http://127.0.0.1:9000" })

	result := ask(t, ctx, conn, "2", "exposed_ports", 0)
	if ports := result["data"].(map[string]any)["ports"].([]any); len(ports) != 0 {
		t.Fatalf("a serve entry the user changed is no longer Muxer's: %v", result)
	}
	result = ask(t, ctx, conn, "3", "unexpose_port", 4000)
	if _, unserved := relay.tailnet.calls(); errorCode(result) != "not_exposed" || len(unserved) != 0 {
		t.Fatalf("the user's replacement must not be removed: %v %v", result, unserved)
	}
}

func TestExposedPortsSurviveAServeStatusThatBrieflyLacksThem(t *testing.T) {
	relay, ctx, conn := portRelay(t, 4000)
	relay.tailnet.listen(4000, "127.0.0.1")
	ask(t, ctx, conn, "1", "expose_port", 4000)
	var target string
	relay.tailnet.update(func(f *fakeTailnet) {
		target = f.proxies[4000]
		delete(f.proxies, 4000)
	})

	result := ask(t, ctx, conn, "2", "exposed_ports", 0)
	if ports := result["data"].(map[string]any)["ports"].([]any); len(ports) != 0 {
		t.Fatalf("a port tailscale doesn't serve must not be listed: %v", result)
	}
	relay.tailnet.update(func(f *fakeTailnet) { f.proxies[4000] = target })
	result = ask(t, ctx, conn, "3", "unexpose_port", 4000)
	if _, unserved := relay.tailnet.calls(); result["ok"] != true || len(unserved) != 1 {
		t.Fatalf("Muxer must still own the port once it's served again: %v %v", result, unserved)
	}
}

func askLinks(t *testing.T, ctx context.Context, conn *websocket.Conn, id, surfaceID string) map[string]any {
	t.Helper()
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "port_links", "id": id, "surface_id": surfaceID})
	return resultFor(t, ctx, conn, id)
}

func TestPortLinksDescribeEveryPortOfATerminal(t *testing.T) {
	relay, ctx, conn := portRelay(t, 3000, 5173, 2222, 8765, 4000)
	relay.tailnet.listen(3000, "::")
	relay.tailnet.listen(5173, "127.0.0.1")
	relay.tailnet.listen(2222, "127.0.0.1")
	relay.tailnet.update(func(f *fakeTailnet) { f.tcpOnly[2222] = true })

	result := askLinks(t, ctx, conn, "1", "t1")
	links, _ := result["data"].(map[string]any)["ports"].([]any)
	if result["ok"] != true || len(links) != 5 {
		t.Fatalf("every port of the terminal must be listed: %v", result)
	}
	byPort := map[float64]map[string]any{}
	for _, link := range links {
		entry := link.(map[string]any)
		byPort[entry["port"].(float64)] = entry
	}
	if byPort[3000]["url"] != "http://mac.tailnet.ts.net:3000" || byPort[3000]["error"] != nil {
		t.Fatalf("a port open on every interface must be reachable: %v", byPort[3000])
	}
	if byPort[5173]["url"] != "https://mac.tailnet.ts.net:5173" || byPort[5173]["needs_expose"] != true {
		t.Fatalf("a loopback port must ask to be exposed: %v", byPort[5173])
	}
	for port, code := range map[float64]string{2222: "serve_conflict", 8765: "port_not_allowed", 4000: "not_listening"} {
		failure, _ := byPort[port]["error"].(map[string]any)
		if failure["code"] != code || byPort[port]["url"] != "" {
			t.Fatalf("port %v must fail with %s: %v", port, code, byPort[port])
		}
	}
	if served, _ := relay.tailnet.calls(); len(served) != 0 {
		t.Fatalf("listing ports must not expose any: %v", served)
	}

	result = askLinks(t, ctx, conn, "2", "gone")
	if links, _ := result["data"].(map[string]any)["ports"].([]any); result["ok"] != true || len(links) != 0 {
		t.Fatalf("an unknown terminal has no ports: %v", result)
	}

	relay.tailnet.update(func(f *fakeTailnet) { f.selfErr = tailnet.ErrNotRunning })
	if result := askLinks(t, ctx, conn, "3", "t1"); errorCode(result) != "tailscale_offline" {
		t.Fatalf("a logged out tailscale must fail the whole list: %v", result)
	}
}
