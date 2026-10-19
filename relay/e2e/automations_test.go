//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const scratchAutomations = `{
  "version": 1,
  "rules": [
    {
      "id": "muxer-e2e-notify",
      "when": { "event": "muxer.e2e.never_fires" },
      "where": { "workspace.title": { "prefix": "e2e-" } },
      "rate_limit": { "interval_seconds": 30, "maximum": 2 },
      "then": [{ "action": "notify", "title": "Muxer e2e rule", "body": "{{payload.agent}}" }]
    },
    {
      "id": "muxer-e2e-off",
      "when": { "category": "muxer.e2e.never" },
      "enabled": false,
      "then": [{ "action": "notify", "title": "Muxer e2e rule that is off" }]
    }
  ]
}
`

func automationsFile(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".cmuxterm", "automations.json")
}

func useScratchAutomations(t *testing.T, contents string) func(string) {
	t.Helper()
	path := automationsFile(t)
	info, err := os.Lstat(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if existed && !info.Mode().IsRegular() {
		t.Skipf("%s is not a regular file, so the test leaves it alone", path)
	}
	var original []byte
	if existed {
		if original, err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	_, dirErr := os.Stat(filepath.Dir(path))
	createdDir := errors.Is(dirErr, fs.ErrNotExist)
	t.Cleanup(func() {
		if existed {
			if err := os.WriteFile(path, original, info.Mode().Perm()); err != nil {
				t.Errorf("could not restore %s: %v", path, err)
			}
			if restored, err := os.ReadFile(path); err != nil || !bytes.Equal(restored, original) {
				t.Errorf("%s doesn't match what was there before the test", path)
			}
		} else {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("could not remove %s: %v", path, err)
			}
			if createdDir {
				_ = os.Remove(filepath.Dir(path))
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		client := cmux.NewClient(cmux.DefaultSocketPath(), cmux.DefaultPasswordSource())
		defer client.Close()
		if err := client.ReloadAutomations(ctx); err != nil {
			t.Errorf("could not reload cmux's automations after restoring them: %v", err)
		}
	})
	write := func(contents string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(contents)
	return write
}

func reloadUntil(t *testing.T, p *phone, what string, done func(message) bool) message {
	t.Helper()
	answer := p.must("reload_automations", nil)
	for deadline := time.Now().Add(10 * time.Second); !done(answer); {
		if time.Now().After(deadline) {
			t.Fatalf("after reloading, %s never showed: %v", what, answer)
		}
		time.Sleep(200 * time.Millisecond)
		answer = p.must("automations", nil)
	}
	return answer
}

func TestAutomationsCanBeReadToggledAndReloaded(t *testing.T) {
	p := connect(t)
	write := useScratchAutomations(t, scratchAutomations)

	listed := reloadUntil(t, p, "the scratch rules", func(answer message) bool {
		return find(list(answer["rules"]), "id", "muxer-e2e-notify") != nil
	})
	rules := list(listed["rules"])
	notify, off := find(rules, "id", "muxer-e2e-notify"), find(rules, "id", "muxer-e2e-off")
	if off == nil || !flag(notify["enabled"]) || flag(off["enabled"]) {
		t.Fatalf("the scratch rules must come back with their states: %v", rules)
	}
	if notify["trigger"] != "muxer.e2e.never_fires" || off["trigger"] != "any event in muxer.e2e.never" {
		t.Fatalf("triggers = %v, %v", notify["trigger"], off["trigger"])
	}
	if conditions := notify["conditions"].([]any); !slices.Equal(conditions, []any{`workspace.title starts with "e2e-"`}) {
		t.Fatalf("conditions = %v", conditions)
	}
	actions := list(notify["actions"])
	if len(actions) != 1 || actions[0]["summary"] != "Notify “Muxer e2e rule”" {
		t.Fatalf("actions = %v", actions)
	}
	if limit := object(notify["rate_limit"]); number(limit["interval_seconds"]) != 30 || number(limit["maximum"]) != 2 {
		t.Fatalf("rate_limit = %v", notify["rate_limit"])
	}

	turnedOff := p.must("set_automation_enabled", map[string]any{"rule_id": "muxer-e2e-notify", "enabled": false})
	if flag(find(list(turnedOff["rules"]), "id", "muxer-e2e-notify")["enabled"]) {
		t.Fatalf("the answer must show the rule off: %v", turnedOff)
	}
	if saved, _ := os.ReadFile(automationsFile(t)); !strings.Contains(string(saved), `"enabled" : false`) {
		t.Fatalf("cmux must have written the change to the file:\n%s", saved)
	}
	turnedOn := p.must("set_automation_enabled", map[string]any{"rule_id": "muxer-e2e-notify", "enabled": true})
	if !flag(find(list(turnedOn["rules"]), "id", "muxer-e2e-notify")["enabled"]) {
		t.Fatalf("the answer must show the rule on again: %v", turnedOn)
	}
	if failure := p.failure("set_automation_enabled", map[string]any{"rule_id": "muxer-e2e-missing", "enabled": true}); failure.Code != "not_found" {
		t.Fatalf("an unknown rule must be not_found: %v", failure)
	}

	var events []string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		logs := list(p.must("automation_logs", map[string]any{"rule_id": "muxer-e2e-notify", "limit": 10})["logs"])
		events = events[:0]
		for _, line := range logs {
			if line["rule_id"] != "muxer-e2e-notify" || line["status"] != "completed" {
				t.Fatalf("logs for one rule must only hold that rule's lines: %v", logs)
			}
			events = append(events, text(line["event"]))
		}
		if len(events) >= 2 && events[0] == "config.enable" && events[1] == "config.disable" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log must show the rule turned off, then on, newest first: %v", events)
		}
	}

	write("{ this is not json")
	broken := reloadUntil(t, p, "the config error", func(answer message) bool {
		return text(answer["config_error"]) != ""
	})
	if len(list(broken["rules"])) != 0 {
		t.Fatalf("a file cmux can't read must leave no rules: %v", broken)
	}
}
