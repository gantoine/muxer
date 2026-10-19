package server

import (
	"context"
	"slices"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const maxParentHops = 64

func (s *Server) canDiscoverPorts() bool {
	return s.options.ListeningProcesses != nil && s.options.ParentProcesses != nil && s.methods.supports(cmux.MethodTop)
}

func (s *Server) discoverPorts(ctx context.Context) (map[string][]int, error) {
	if !s.canDiscoverPorts() {
		return nil, nil
	}
	listening, err := s.options.ListeningProcesses(ctx)
	if err != nil || len(listening) == 0 {
		return nil, err
	}
	top, err := guarded(s, cmux.MethodTop, errMethodMissing, func() (cmux.Top, error) { return s.cmux.Top(ctx) })
	if err != nil {
		return nil, err
	}
	parents, err := s.options.ParentProcesses(ctx)
	if err != nil {
		return nil, err
	}
	return portsBySurface(surfaceOwners(top), parents, listening), nil
}

func surfaceOwners(top cmux.Top) map[int]string {
	owners := map[int]string{}
	var walk func(surfaceID string, processes []cmux.TopProcess)
	walk = func(surfaceID string, processes []cmux.TopProcess) {
		for _, process := range processes {
			owners[process.PID] = surfaceID
			walk(surfaceID, process.Children)
		}
	}
	for _, window := range top.Windows {
		for _, workspace := range window.Workspaces {
			for _, pane := range workspace.Panes {
				for _, surface := range pane.Surfaces {
					if surface.ID != "" {
						walk(surface.ID, surface.Processes)
					}
				}
			}
		}
	}
	return owners
}

func portsBySurface(owners map[int]string, parents map[int]int, listening map[int][]int) map[string][]int {
	found := map[string][]int{}
	for pid, ports := range listening {
		surfaceID := ownerOf(pid, owners, parents)
		if surfaceID == "" {
			continue
		}
		for _, port := range ports {
			if !slices.Contains(found[surfaceID], port) {
				found[surfaceID] = append(found[surfaceID], port)
			}
		}
	}
	for _, ports := range found {
		slices.Sort(ports)
	}
	return found
}

func ownerOf(pid int, owners map[int]string, parents map[int]int) string {
	for hop := 0; hop < maxParentHops && pid > 1; hop++ {
		if surfaceID, ok := owners[pid]; ok {
			return surfaceID
		}
		parent, ok := parents[pid]
		if !ok || parent == pid {
			return ""
		}
		pid = parent
	}
	return ""
}

func needsDiscovery(reported map[string]cmux.TerminalGit, surfaceIDs []string) bool {
	for _, surfaceID := range surfaceIDs {
		if len(reported[surfaceID].ListeningPorts) == 0 {
			return true
		}
	}
	return false
}

func mergedPorts(reported []int, discovered []int) []int {
	if len(reported) > 0 {
		return reported
	}
	return discovered
}
