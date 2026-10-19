package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

func lsofListeners(ctx context.Context, filter string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "lsof", "-nP", filter, "-sTCP:LISTEN", "-Fpn").Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
		return nil, nil
	}
	return output, err
}

func Listeners(ctx context.Context, port int) ([]netip.Addr, error) {
	output, err := lsofListeners(ctx, "-iTCP:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	return ParseListeners(output, port), nil
}

func ListeningProcesses(ctx context.Context) (map[int][]int, error) {
	output, err := lsofListeners(ctx, "-iTCP")
	if err != nil {
		return nil, err
	}
	return ParseListeningProcesses(output), nil
}

func ParentProcesses(ctx context.Context) (map[int]int, error) {
	output, err := exec.CommandContext(ctx, "/bin/ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	return ParseParentProcesses(output), nil
}

func ParseListeners(lsof []byte, port int) []netip.Addr {
	suffix := ":" + strconv.Itoa(port)
	var addrs []netip.Addr
	scanner := bufio.NewScanner(bytes.NewReader(lsof))
	for scanner.Scan() {
		name, found := strings.CutPrefix(scanner.Text(), "n")
		if !found {
			continue
		}
		host, found := strings.CutSuffix(name, suffix)
		if !found {
			continue
		}
		var addr netip.Addr
		if host == "*" {
			addr = netip.IPv6Unspecified()
		} else {
			parsed, err := netip.ParseAddr(strings.Trim(host, "[]"))
			if err != nil {
				continue
			}
			addr = parsed.Unmap()
		}
		if !slices.Contains(addrs, addr) {
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

func ParseListeningProcesses(lsof []byte) map[int][]int {
	ports := map[int][]int{}
	pid := 0
	scanner := bufio.NewScanner(bytes.NewReader(lsof))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && pid > 0:
			separator := strings.LastIndex(line, ":")
			port, err := strconv.Atoi(line[separator+1:])
			if separator < 0 || err != nil || slices.Contains(ports[pid], port) {
				continue
			}
			ports[pid] = append(ports[pid], port)
		}
	}
	return ports
}

func ParseParentProcesses(ps []byte) map[int]int {
	parents := map[int]int{}
	scanner := bufio.NewScanner(bytes.NewReader(ps))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr == nil && parentErr == nil {
			parents[pid] = parent
		}
	}
	return parents
}
