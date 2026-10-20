package pi

import (
	"os"
	"path/filepath"
	"regexp"
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

func AgentDir() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

func FindSession(agentDir, sessionID string) (string, bool) {
	if agentDir == "" || !sessionIDPattern.MatchString(sessionID) {
		return "", false
	}
	matches, _ := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*_"+sessionID+".jsonl"))
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() {
			return match, true
		}
	}
	return "", false
}
