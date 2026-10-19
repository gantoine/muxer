package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"

	"github.com/gantoine/muxer/relay/internal/cmux"
	"github.com/gantoine/muxer/relay/internal/tailnet"
)

type Tailnet interface {
	Self(ctx context.Context) (tailnet.Self, error)
	ServeConfig(ctx context.Context) (tailnet.ServeConfig, error)
	Serve(ctx context.Context, port int, target string) error
	Unserve(ctx context.Context, port int) error
}

type PortLink struct {
	Port        int    `json:"port"`
	URL         string `json:"url"`
	Exposed     bool   `json:"exposed"`
	NeedsExpose bool   `json:"needs_expose"`
}

type ExposedPort struct {
	Port int    `json:"port"`
	URL  string `json:"url"`
}

type exposure struct {
	Port   int    `json:"port"`
	Target string `json:"target"`
}

type portExposures struct {
	mu      sync.Mutex
	path    string
	entries []exposure
	loaded  bool
}

func (p *portExposures) load() {
	if p.loaded {
		return
	}
	p.loaded = true
	if p.path == "" {
		return
	}
	data, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var stored struct {
		Ports []exposure `json:"ports"`
	}
	if json.Unmarshal(data, &stored) == nil {
		p.entries = stored.Ports
	}
}

func (p *portExposures) save() error {
	if p.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(map[string]any{"ports": p.entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.path, data, 0o600)
}

func (p *portExposures) find(port int) (exposure, bool) {
	index := slices.IndexFunc(p.entries, func(entry exposure) bool { return entry.Port == port })
	if index < 0 {
		return exposure{}, false
	}
	return p.entries[index], true
}

func (p *portExposures) remove(port int) {
	p.entries = slices.DeleteFunc(p.entries, func(entry exposure) bool { return entry.Port == port })
}

var (
	errPortsUnsupported = unsupported("report terminal ports")
	errNoTailnet        = &requestError{Code: "unsupported", Message: "This relay can't open ports."}
)

func portError(code, message string) error {
	return &requestError{Code: code, Message: message}
}

func tailnetError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, tailnet.ErrNotInstalled):
		return portError("tailscale_missing", "Tailscale isn't installed on the computer. Opening ports needs Tailscale.")
	case errors.Is(err, tailnet.ErrNotRunning):
		return portError("tailscale_offline", "Tailscale isn't running or logged in on the computer.")
	default:
		return portError("tailscale_failed", err.Error())
	}
}

func validPort(port int) error {
	if port < 1 || port > 65535 {
		return invalid("Send a port between 1 and 65535.")
	}
	return nil
}

func (s *Server) checkTerminalPort(ctx context.Context, port int) error {
	if port == s.options.RelayPort {
		return portError("port_not_allowed", "That port is the relay itself.")
	}
	terminals, err := guarded(s, cmux.MethodTerminalGit, errPortsUnsupported, func() ([]cmux.TerminalGit, error) {
		return s.cmux.TerminalGit(ctx)
	})
	if err != nil {
		return err
	}
	for _, terminal := range terminals {
		if slices.Contains(terminal.ListeningPorts, port) {
			return nil
		}
	}
	return portError("port_not_allowed", "No terminal is listening on port "+strconv.Itoa(port)+".")
}

type portState struct {
	self   tailnet.Self
	config tailnet.ServeConfig
}

func (s *Server) readTailnet(ctx context.Context) (portState, error) {
	if s.options.Tailnet == nil {
		return portState{}, errNoTailnet
	}
	self, err := s.options.Tailnet.Self(ctx)
	if err != nil {
		return portState{}, tailnetError(err)
	}
	config, err := s.options.Tailnet.ServeConfig(ctx)
	if err != nil {
		return portState{}, tailnetError(err)
	}
	return portState{self: self, config: config}, nil
}

func (state portState) url(scheme string, port int) string {
	return scheme + "://" + net.JoinHostPort(state.self.DNSName, strconv.Itoa(port))
}

func (state portState) serves(entry exposure) bool {
	return state.config.HTTPSProxy(state.self.DNSName, entry.Port) == entry.Target
}

func (s *Server) owns(state portState, port int) bool {
	entry, found := s.exposures.find(port)
	return found && state.serves(entry)
}

func (s *Server) resolvePort(ctx context.Context, port int) (PortLink, string, error) {
	if err := validPort(port); err != nil {
		return PortLink{}, "", err
	}
	if err := s.checkTerminalPort(ctx, port); err != nil {
		return PortLink{}, "", err
	}
	state, err := s.readTailnet(ctx)
	if err != nil {
		return PortLink{}, "", err
	}
	s.exposures.load()
	if s.owns(state, port) {
		return PortLink{Port: port, URL: state.url("https", port), Exposed: true}, "", nil
	}
	if state.config.Uses(port) {
		proxy := state.config.HTTPSProxy(state.self.DNSName, port)
		if s.proxiesToRelay(proxy) {
			return PortLink{}, "", portError("port_not_allowed", "That port is the relay itself.")
		}
		if proxy != "" {
			return PortLink{Port: port, URL: state.url("https", port)}, "", nil
		}
		return PortLink{}, "", portError("serve_conflict", "Tailscale already serves something else on port "+strconv.Itoa(port)+".")
	}
	if s.options.Listeners == nil {
		return PortLink{}, "", errNoTailnet
	}
	listeners, err := s.options.Listeners(ctx, port)
	if err != nil {
		return PortLink{}, "", portError("port_check_failed", "Could not check what listens on port "+strconv.Itoa(port)+": "+err.Error())
	}
	if len(listeners) == 0 {
		return PortLink{}, "", portError("not_listening", "Nothing is listening on port "+strconv.Itoa(port)+" anymore.")
	}
	if slices.ContainsFunc(listeners, func(addr netip.Addr) bool { return addr.IsUnspecified() || state.self.Has(addr) }) {
		return PortLink{Port: port, URL: state.url("http", port)}, "", nil
	}
	return PortLink{Port: port, URL: state.url("https", port), NeedsExpose: true}, exposeTarget(listeners, port), nil
}

func (s *Server) proxiesToRelay(proxy string) bool {
	target, err := url.Parse(proxy)
	return err == nil && target.Port() == strconv.Itoa(s.options.RelayPort)
}

func exposeTarget(listeners []netip.Addr, port int) string {
	chosen := listeners[0]
	for _, preferred := range []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.IPv6Loopback()} {
		if slices.Contains(listeners, preferred) {
			chosen = preferred
			break
		}
	}
	return "http://" + net.JoinHostPort(chosen.String(), strconv.Itoa(port))
}

func (s *Server) portLink(ctx context.Context, port int, expose bool) (PortLink, error) {
	s.exposures.mu.Lock()
	defer s.exposures.mu.Unlock()
	link, target, err := s.resolvePort(ctx, port)
	if err != nil || !expose || !link.NeedsExpose {
		return link, err
	}
	if err := s.options.Tailnet.Serve(ctx, port, target); err != nil {
		return PortLink{}, tailnetError(err)
	}
	s.exposures.remove(port)
	s.exposures.entries = append(s.exposures.entries, exposure{Port: port, Target: target})
	if err := s.exposures.save(); err != nil {
		s.logger.Warn("could not remember exposed port", "port", port, "error", err)
	}
	s.logger.Info("exposed port on the tailnet", "port", port, "target", target)
	return PortLink{Port: port, URL: link.URL, Exposed: true}, nil
}

func (s *Server) unexposePort(ctx context.Context, port int) ([]ExposedPort, error) {
	if err := validPort(port); err != nil {
		return nil, err
	}
	s.exposures.mu.Lock()
	defer s.exposures.mu.Unlock()
	state, err := s.readTailnet(ctx)
	if err != nil {
		return nil, err
	}
	s.exposures.load()
	if _, recorded := s.exposures.find(port); !recorded {
		return nil, portError("not_exposed", "Muxer didn't expose port "+strconv.Itoa(port)+".")
	}
	if s.owns(state, port) {
		if err := s.options.Tailnet.Unserve(ctx, port); err != nil {
			return nil, tailnetError(err)
		}
		s.logger.Info("stopped exposing port on the tailnet", "port", port)
	}
	s.exposures.remove(port)
	if err := s.exposures.save(); err != nil {
		s.logger.Warn("could not forget exposed port", "port", port, "error", err)
	}
	return s.listExposed(state), nil
}

func (s *Server) exposedPorts(ctx context.Context) ([]ExposedPort, error) {
	s.exposures.mu.Lock()
	defer s.exposures.mu.Unlock()
	state, err := s.readTailnet(ctx)
	if err != nil {
		return nil, err
	}
	s.exposures.load()
	before := len(s.exposures.entries)
	s.exposures.entries = slices.DeleteFunc(s.exposures.entries, func(entry exposure) bool { return !state.serves(entry) })
	if len(s.exposures.entries) != before {
		if err := s.exposures.save(); err != nil {
			s.logger.Warn("could not forget removed ports", "error", err)
		}
	}
	return s.listExposed(state), nil
}

func (s *Server) listExposed(state portState) []ExposedPort {
	ports := []ExposedPort{}
	for _, entry := range s.exposures.entries {
		if state.serves(entry) {
			ports = append(ports, ExposedPort{Port: entry.Port, URL: state.url("https", entry.Port)})
		}
	}
	slices.SortFunc(ports, func(a, b ExposedPort) int { return a.Port - b.Port })
	return ports
}
