package host

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	defaultShell      = "/bin/zsh"
	agentMarker       = "muxer-agent:"
	agentLookupLimit  = 5 * time.Second
	agentLookupMaxAge = 5 * time.Minute
)

func LoginShell() string {
	if shell := os.Getenv("SHELL"); strings.HasPrefix(shell, "/") {
		return shell
	}
	return defaultShell
}

type AgentFinder struct {
	shell string
	names []string

	mu      sync.Mutex
	found   []string
	foundAt time.Time
}

func NewAgentFinder(shell string, names ...string) *AgentFinder {
	return &AgentFinder{shell: shell, names: names}
}

func (f *AgentFinder) Installed(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.foundAt.IsZero() && time.Since(f.foundAt) < agentLookupMaxAge {
		return slices.Clone(f.found), nil
	}
	ctx, cancel := context.WithTimeout(ctx, agentLookupLimit)
	defer cancel()
	command := exec.CommandContext(ctx, f.shell, "-lic", AgentLookupScript(f.names))
	command.WaitDelay = time.Second
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	f.found = FoundAgents(output, f.names)
	f.foundAt = time.Now()
	return slices.Clone(f.found), nil
}

func AgentLookupScript(names []string) string {
	var script strings.Builder
	for _, name := range names {
		script.WriteString("command -v " + name + " >/dev/null 2>&1 && printf '%s\\n' '" + agentMarker + name + "'\n")
	}
	script.WriteString("exit 0\n")
	return script.String()
}

func FoundAgents(output []byte, names []string) []string {
	found := []string{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		_, name, ok := strings.Cut(strings.TrimSpace(scanner.Text()), agentMarker)
		if ok && slices.Contains(names, name) && !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	slices.SortFunc(found, func(a, b string) int { return slices.Index(names, a) - slices.Index(names, b) })
	return found
}
