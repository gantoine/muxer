package files

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MaxMatches  = 30
	maxEntries  = 20000
	maxDepth    = 8
	cacheTTL    = 10 * time.Second
	listTimeout = 3 * time.Second
)

var skippedDirs = map[string]bool{"node_modules": true, "build": true, "dist": true, "target": true, "__pycache__": true}

var errNoFolder = errors.New("no folder to list")

type Index struct {
	mu     sync.Mutex
	cached map[string]listing
	now    func() time.Time
}

type listing struct {
	at    time.Time
	paths []string
}

func NewIndex() *Index {
	return &Index{cached: map[string]listing{}, now: time.Now}
}

func (i *Index) Search(ctx context.Context, root, query string) ([]string, error) {
	paths, err := i.paths(ctx, root)
	if err != nil {
		return nil, err
	}
	return Rank(paths, query, MaxMatches), nil
}

func (i *Index) paths(ctx context.Context, root string) ([]string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, errNoFolder
	}
	i.mu.Lock()
	cached, ok := i.cached[root]
	i.mu.Unlock()
	if ok && i.now().Sub(cached.at) < cacheTTL {
		return cached.paths, nil
	}
	paths, err := gitFiles(ctx, root)
	if err != nil {
		paths, err = walk(root)
	}
	if err != nil {
		return nil, err
	}
	paths = withDirectories(paths)
	now := i.now()
	i.mu.Lock()
	for cachedRoot, entry := range i.cached {
		if now.Sub(entry.at) >= cacheTTL {
			delete(i.cached, cachedRoot)
		}
	}
	i.cached[root] = listing{at: now, paths: paths}
	i.mu.Unlock()
	return paths, nil
}

func gitFiles(ctx context.Context, root string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) > 0 {
			paths = append(paths, string(name))
		}
		if len(paths) == maxEntries {
			break
		}
	}
	return paths, nil
}

func walk(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil || current == root {
			return nil
		}
		rel, _ := filepath.Rel(root, current)
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || skippedDirs[entry.Name()] || strings.Count(rel, "/") >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		paths = append(paths, rel)
		if len(paths) == maxEntries {
			return filepath.SkipAll
		}
		return nil
	})
	return paths, err
}

func withDirectories(paths []string) []string {
	seen := map[string]bool{}
	all := append([]string(nil), paths...)
	for _, file := range paths {
		for dir := path.Dir(file); dir != "." && dir != "/" && !seen[dir]; dir = path.Dir(dir) {
			seen[dir] = true
			all = append(all, dir+"/")
		}
	}
	return all
}

func Rank(paths []string, query string, limit int) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	type match struct {
		path  string
		score int
	}
	var matches []match
	for _, candidate := range paths {
		if score, ok := score(candidate, query); ok {
			matches = append(matches, match{candidate, score})
		}
	}
	sort.Slice(matches, func(a, b int) bool {
		if matches[a].score != matches[b].score {
			return matches[a].score < matches[b].score
		}
		depthA, depthB := strings.Count(strings.TrimSuffix(matches[a].path, "/"), "/"), strings.Count(strings.TrimSuffix(matches[b].path, "/"), "/")
		if depthA != depthB {
			return depthA < depthB
		}
		if len(matches[a].path) != len(matches[b].path) {
			return len(matches[a].path) < len(matches[b].path)
		}
		return matches[a].path < matches[b].path
	})
	ranked := make([]string, 0, min(limit, len(matches)))
	for _, m := range matches[:min(limit, len(matches))] {
		ranked = append(ranked, m.path)
	}
	return ranked
}

func score(candidate, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	lower := strings.ToLower(candidate)
	base := path.Base(strings.TrimSuffix(lower, "/"))
	switch {
	case base == query:
		return 0, true
	case strings.HasPrefix(base, query):
		return 1, true
	case strings.Contains(base, query):
		return 2, true
	case strings.Contains(lower, query):
		return 3, true
	case isSubsequence(query, lower):
		return 4, true
	}
	return 0, false
}

func isSubsequence(query, text string) bool {
	wanted := []rune(query)
	position := 0
	for _, r := range text {
		if position < len(wanted) && wanted[position] == r {
			position++
		}
	}
	return position == len(wanted)
}
