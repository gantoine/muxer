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

func Listeners(ctx context.Context, port int) ([]netip.Addr, error) {
	output, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fn").Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseListeners(output, port), nil
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
