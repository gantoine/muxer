package tailnet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var (
	ErrNotInstalled = errors.New("tailscale is not installed; install it from https://tailscale.com/download")
	ErrNotRunning   = errors.New("tailscale is not running or not logged in")
)

var binaryCandidates = []string{
	"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	"/usr/local/bin/tailscale",
	"/opt/homebrew/bin/tailscale",
}

func Binary() (string, error) {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path, nil
	}
	for _, candidate := range binaryCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", ErrNotInstalled
}

type Self struct {
	DNSName string
	IPs     []netip.Addr
}

func (s Self) Has(addr netip.Addr) bool {
	for _, ip := range s.IPs {
		if ip == addr.Unmap() {
			return true
		}
	}
	return false
}

func ParseStatus(output []byte) (Self, error) {
	var status struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			DNSName      string       `json:"DNSName"`
			TailscaleIPs []netip.Addr `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(output, &status); err != nil {
		return Self{}, fmt.Errorf("could not read `tailscale status`: %w", err)
	}
	name := strings.TrimSuffix(status.Self.DNSName, ".")
	if status.BackendState != "Running" || name == "" {
		return Self{}, ErrNotRunning
	}
	return Self{DNSName: name, IPs: status.Self.TailscaleIPs}, nil
}

type ServeConfig struct {
	TCP map[string]struct {
		HTTPS      bool   `json:"HTTPS"`
		HTTP       bool   `json:"HTTP"`
		TCPForward string `json:"TCPForward"`
	} `json:"TCP"`
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

func ParseServeConfig(output []byte) (ServeConfig, error) {
	var config ServeConfig
	if len(bytes.TrimSpace(output)) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(output, &config); err != nil {
		return ServeConfig{}, fmt.Errorf("could not read `tailscale serve status`: %w", err)
	}
	return config, nil
}

func (c ServeConfig) Uses(port int) bool {
	_, found := c.TCP[strconv.Itoa(port)]
	return found
}

func (c ServeConfig) HTTPSProxy(dnsName string, port int) string {
	listener, found := c.TCP[strconv.Itoa(port)]
	if !found || !listener.HTTPS {
		return ""
	}
	handlers := c.Web[dnsName+":"+strconv.Itoa(port)].Handlers
	if len(handlers) != 1 {
		return ""
	}
	return handlers["/"].Proxy
}

type CLI struct{}

func (CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	binary, err := Binary()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, binary, args...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return nil, fmt.Errorf("tailscale %s: %s", args[0], message)
		}
		return nil, fmt.Errorf("tailscale %s: %w", args[0], err)
	}
	return output, nil
}

func (c CLI) Self(ctx context.Context) (Self, error) {
	output, err := c.run(ctx, "status", "--json")
	if errors.Is(err, ErrNotInstalled) {
		return Self{}, err
	}
	if err != nil {
		return Self{}, ErrNotRunning
	}
	return ParseStatus(output)
}

func (c CLI) ServeConfig(ctx context.Context) (ServeConfig, error) {
	output, err := c.run(ctx, "serve", "status", "--json")
	if err != nil {
		return ServeConfig{}, err
	}
	return ParseServeConfig(output)
}

func (c CLI) Serve(ctx context.Context, port int, target string) error {
	_, err := c.run(ctx, "serve", "--bg", "--https="+strconv.Itoa(port), target)
	return err
}

func (c CLI) Unserve(ctx context.Context, port int) error {
	_, err := c.run(ctx, "serve", "--https="+strconv.Itoa(port), "off")
	return err
}
