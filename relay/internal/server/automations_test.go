package server

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const automationRulesFixture = `[
  {
    "id": "phone-auto-resume",
    "when": {"event": "agent.auto_resume.sent"},
    "where": {"agent": "claude", "workspace.title": {"prefix": "api-", "not": {"contains": "scratch"}}, "surface.kind": {"in": ["terminal", "browser"]}},
    "then": [
      {"action": "notify", "title": "Agent resumed", "body": "{{payload.agent}} was resumed"},
      {"action": "rpc", "method": "workspace.reorder", "params": {"index": 1}}
    ],
    "rate_limit": {"interval_seconds": 30, "maximum": 1}
  },
  {
    "id": "agents-anything",
    "when": {"category": "agent"},
    "then": [{"action": "webhook", "url": "https://example.com/hook", "headers": "[redacted]"}],
    "enabled": false
  }
]`

func (f *fakeCmux) Automations(context.Context) ([]cmux.AutomationSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.automationCalls = append(f.automationCalls, "list")
	if f.automationErr != nil {
		return nil, f.automationErr
	}
	summaries := make([]cmux.AutomationSummary, len(f.automationRules))
	for index, rule := range f.automationRules {
		summaries[index] = cmux.AutomationSummary{
			ID:          rule.ID,
			Enabled:     rule.Enabled == nil || *rule.Enabled,
			ActionCount: len(rule.Then),
			RateLimit:   orDefault(rule.RateLimit, &cmux.AutomationRateLimit{IntervalSeconds: 1, Maximum: 1}),
		}
		if rule.When.Event != "" {
			summaries[index].Event = &rule.When.Event
		}
		if rule.When.Category != "" {
			summaries[index].Category = &rule.When.Category
		}
	}
	return summaries, nil
}

func orDefault[T any](value, fallback *T) *T {
	if value != nil {
		return value
	}
	return fallback
}

func (f *fakeCmux) Automation(_ context.Context, id string) (cmux.AutomationRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.automationCalls = append(f.automationCalls, "show "+id)
	for _, rule := range f.automationRules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return cmux.AutomationRule{}, &cmux.RPCError{Code: "not_found", Message: "Automation rule not found: " + id}
}

func (f *fakeCmux) SetAutomationEnabled(_ context.Context, id string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.automationCalls = append(f.automationCalls, "set "+id)
	event := "config.disable"
	if enabled {
		event = "config.enable"
	}
	for index := range f.automationRules {
		if f.automationRules[index].ID == id {
			f.automationRules[index].Enabled = &enabled
			f.automationLogs = append(f.automationLogs, cmux.AutomationLog{
				OccurredAt: time.Now().UTC().Format(time.RFC3339), RuleID: id, Event: event, Status: "completed", Detail: "rule changed",
			})
			return nil
		}
	}
	return &cmux.RPCError{Code: "not_found", Message: "Automation rule not found: " + id}
}

func (f *fakeCmux) AutomationLogs(_ context.Context, limit int) ([]cmux.AutomationLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.automationCalls = append(f.automationCalls, "logs")
	logs := f.automationLogs
	if len(logs) > limit {
		logs = logs[len(logs)-limit:]
	}
	return append([]cmux.AutomationLog(nil), logs...), nil
}

func (f *fakeCmux) ReloadAutomations(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.automationCalls = append(f.automationCalls, "reload")
	if f.reloadedRules == nil {
		f.automationRules = nil
		f.automationLogs = append(f.automationLogs, cmux.AutomationLog{
			OccurredAt: time.Now().UTC().Format(time.RFC3339), Event: "config.reload", Status: "error", Detail: "The data couldn't be read because it isn't in the correct format.",
		})
		return nil
	}
	f.automationRules = f.reloadedRules
	return nil
}

func automationRelay(t *testing.T) testRelay {
	t.Helper()
	relay := startRelay(t)
	if err := json.Unmarshal([]byte(automationRulesFixture), &relay.cmux.automationRules); err != nil {
		t.Fatal(err)
	}
	relay.cmux.automationLogs = []cmux.AutomationLog{
		{OccurredAt: "2026-10-18T09:00:00.000Z", RuleID: "phone-auto-resume", Event: "agent.auto_resume.sent", Status: "started"},
		{OccurredAt: "2026-10-18T09:00:00.120Z", RuleID: "phone-auto-resume", Event: "agent.auto_resume.sent", Status: "completed", Detail: "2 actions"},
		{OccurredAt: "2026-10-18T09:05:00.000Z", RuleID: "phone-auto-resume", Event: "config.disable", Status: "completed", Detail: "rule disabled"},
		{OccurredAt: "2026-10-18T09:06:00.000Z", RuleID: "other", Event: "agent.needs_input", Status: "skipped_rate_limit"},
	}
	return relay
}

func TestPhoneSeesAutomationsInPlainWords(t *testing.T) {
	relay := automationRelay(t)
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "automations", "id": "2"})
	result := resultFor(t, ctx, conn, "2")
	if result["ok"] != true {
		t.Fatalf("automations must succeed: %v", result)
	}
	rules := result["data"].(map[string]any)["rules"].([]any)
	resume, webhook := rules[0].(map[string]any), rules[1].(map[string]any)
	if resume["trigger"] != "agent.auto_resume.sent" || resume["enabled"] != true {
		t.Fatalf("a rule must carry its trigger and state: %v", resume)
	}
	conditions := resume["conditions"].([]any)
	want := []any{
		`agent is "claude"`,
		`surface.kind is one of "terminal", "browser"`,
		`not (workspace.title contains "scratch") and workspace.title starts with "api-"`,
	}
	if !slices.Equal(conditions, want) {
		t.Fatalf("conditions = %v, want %v", conditions, want)
	}
	actions := resume["actions"].([]any)
	notify, rpc := actions[0].(map[string]any), actions[1].(map[string]any)
	if notify["summary"] != "Notify “Agent resumed”" || rpc["summary"] != "Call workspace.reorder" {
		t.Fatalf("actions must be summed up: %v", actions)
	}
	fields := notify["fields"].([]any)
	if first := fields[0].(map[string]any); first["name"] != "title" || first["value"] != "Agent resumed" {
		t.Fatalf("notify fields must start with the title: %v", fields)
	}
	if params := rpc["fields"].([]any)[1].(map[string]any); params["name"] != "params" || params["value"] != `{"index":1}` {
		t.Fatalf("structured fields must be shown as JSON: %v", rpc["fields"])
	}
	lastRun := resume["last_run"].(map[string]any)
	if lastRun["status"] != "completed" || lastRun["event"] != "agent.auto_resume.sent" {
		t.Fatalf("the last run must be the latest firing, not a config change: %v", lastRun)
	}
	if webhook["trigger"] != "any event in agent" || webhook["enabled"] != false || webhook["last_run"] != nil {
		t.Fatalf("a disabled category rule must say so: %v", webhook)
	}
	if summary := webhook["actions"].([]any)[0].(map[string]any)["summary"]; summary != "Send a webhook to https://example.com/hook" {
		t.Fatalf("summary = %v", summary)
	}
}

func TestPhoneTurnsAnAutomationOffAndReadsItsLogs(t *testing.T) {
	relay := automationRelay(t)
	ctx, conn := relay.connect(t)
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "set_automation_enabled", "id": "2", "enabled": false})
	if result := resultFor(t, ctx, conn, "2"); errorCode(result) != "invalid_params" {
		t.Fatalf("set_automation_enabled without a rule must be refused: %v", result)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "set_automation_enabled", "id": "3", "rule_id": "phone-auto-resume", "enabled": false})
	result := resultFor(t, ctx, conn, "3")
	if result["ok"] != true || result["data"].(map[string]any)["rules"].([]any)[0].(map[string]any)["enabled"] != false {
		t.Fatalf("the answer must be the list with the rule turned off: %v", result)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "automation_logs", "id": "4", "rule_id": "phone-auto-resume", "limit": 2})
	result = resultFor(t, ctx, conn, "4")
	logs := result["data"].(map[string]any)["logs"].([]any)
	if len(logs) != 2 || logs[0].(map[string]any)["event"] != "config.disable" || logs[0].(map[string]any)["detail"] != "rule changed" {
		t.Fatalf("logs must be the rule's own, newest first: %v", logs)
	}
}

func TestAutomationLogsAndUnknownRules(t *testing.T) {
	relay := automationRelay(t)
	server := relay.server
	ctx := context.Background()

	all, _ := server.automationLogs(ctx, "", 0)
	if len(all.Logs) != 4 || all.Logs[len(all.Logs)-1].Status != "started" {
		t.Fatalf("without an id every log line must come back: %+v", all.Logs)
	}

	if _, err := server.setAutomationEnabled(ctx, "missing", true); cmux.ErrorCode(err) != "not_found" {
		t.Fatalf("an unknown rule must be not_found, got %v", err)
	}
}

func TestReloadingABrokenFileExplainsTheEmptyList(t *testing.T) {
	previous := automationReloadDelay
	automationReloadDelay = time.Millisecond
	t.Cleanup(func() { automationReloadDelay = previous })
	relay := automationRelay(t)
	ctx := context.Background()
	writeAutomationsFile(t, relay, time.Now().Add(-time.Minute))

	broken, err := relay.server.reloadAutomations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(broken.Rules) != 0 || broken.ConfigError == "" {
		t.Fatalf("a file cmux can't read must come with its error: %+v", broken)
	}

	relay.cmux.mu.Lock()
	relay.cmux.reloadedRules = []cmux.AutomationRule{{ID: "fixed", When: cmux.AutomationWhen{Event: "agent.needs_input"}, Then: []map[string]any{{"action": "notify"}}}}
	relay.cmux.mu.Unlock()
	fixed, err := relay.server.reloadAutomations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed.Rules) != 1 || fixed.ConfigError != "" || fixed.Rules[0].Actions[0].Summary != "Notify" {
		t.Fatalf("a fixed file must list its rules without the old error: %+v", fixed)
	}
}

func TestAnOldReloadErrorIsNotShownForAFileThatChangedOrIsGone(t *testing.T) {
	relay := automationRelay(t)
	ctx := context.Background()
	relay.cmux.mu.Lock()
	relay.cmux.automationRules = nil
	relay.cmux.automationLogs = append(relay.cmux.automationLogs, cmux.AutomationLog{
		OccurredAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), Event: "config.reload", Status: "error", Detail: "Unexpected character",
	})
	relay.cmux.mu.Unlock()

	gone, err := relay.server.automations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gone.ConfigError != "" {
		t.Fatalf("without a rules file there is nothing to fix: %+v", gone)
	}

	writeAutomationsFile(t, relay, time.Now())
	changed, err := relay.server.automations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ConfigError != "" {
		t.Fatalf("an error from before the file last changed is stale: %+v", changed)
	}

	writeAutomationsFile(t, relay, time.Now().Add(-2*time.Hour))
	broken, err := relay.server.automations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if broken.ConfigError != "Unexpected character" {
		t.Fatalf("an error newer than the file must still show: %+v", broken)
	}
}

func writeAutomationsFile(t *testing.T, relay testRelay, modified time.Time) {
	t.Helper()
	path := relay.server.options.AutomationsFile
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationsNeedACmuxThatHasThem(t *testing.T) {
	relay := automationRelay(t)
	relay.cmux.mu.Lock()
	relay.cmux.methods = []string{"system.identify", "mobile.workspace.list", cmux.MethodAutomationList, cmux.MethodAutomationShow}
	relay.cmux.mu.Unlock()
	ctx, conn := relay.connect(t)

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "hello", "id": "1"})
	unsupported := resultFor(t, ctx, conn, "1")["data"].(map[string]any)["unsupported"].([]any)
	for _, kind := range []any{"set_automation_enabled", "automation_logs", "reload_automations"} {
		if !slices.Contains(unsupported, kind) {
			t.Fatalf("the welcome must list %v as unsupported: %v", kind, unsupported)
		}
	}
	if slices.Contains(unsupported, any("automations")) {
		t.Fatalf("listing must stay available: %v", unsupported)
	}

	_ = wsjson.Write(ctx, conn, map[string]any{"type": "automations", "id": "2"})
	result := resultFor(t, ctx, conn, "2")
	if result["ok"] != true || result["data"].(map[string]any)["rules"].([]any)[0].(map[string]any)["last_run"] != nil {
		t.Fatalf("without logs the list must still come back, without last runs: %v", result)
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"type": "reload_automations", "id": "3"})
	if result := resultFor(t, ctx, conn, "3"); errorCode(result) != "unsupported" {
		t.Fatalf("reload must be unsupported: %v", result)
	}
	relay.cmux.mu.Lock()
	defer relay.cmux.mu.Unlock()
	if slices.Contains(relay.cmux.automationCalls, "logs") || slices.Contains(relay.cmux.automationCalls, "reload") {
		t.Fatalf("the relay must not call unlisted methods: %v", relay.cmux.automationCalls)
	}
}
