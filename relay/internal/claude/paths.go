package claude

import (
	"os"
	"path/filepath"
	"regexp"
)

var unsafePathCharacters = regexp.MustCompile(`[^a-zA-Z0-9]`)
var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

func ConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func FindTranscript(configDir, cwd, sessionID string) (string, bool) {
	if configDir == "" || !sessionIDPattern.MatchString(sessionID) {
		return "", false
	}
	projects := filepath.Join(configDir, "projects")
	direct := filepath.Join(projects, unsafePathCharacters.ReplaceAllString(cwd, "-"), sessionID+".jsonl")
	if info, err := os.Stat(direct); err == nil && info.Mode().IsRegular() {
		return direct, true
	}
	matches, _ := filepath.Glob(filepath.Join(projects, "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}
