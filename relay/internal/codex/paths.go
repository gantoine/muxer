package codex

import (
	"os"
	"path/filepath"
	"regexp"
)

var threadIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

func HomeDir() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func FindRollout(homeDir, threadID string) (string, bool) {
	if homeDir == "" || !threadIDPattern.MatchString(threadID) {
		return "", false
	}
	matches, _ := filepath.Glob(filepath.Join(homeDir, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() {
			return match, true
		}
	}
	return "", false
}
