package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

const (
	automationLogCapacity   = 256
	defaultAutomationLogs   = 100
	automationSummaryLength = 80
	configReloadEvent       = "config.reload"
)

var (
	automationSettleTimeout = 2 * time.Second
	automationSettleStep    = 100 * time.Millisecond
	automationReloadDelay   = 400 * time.Millisecond
)

var errAutomationsUnsupported = unsupported("manage automations")

type automationField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type automationActionView struct {
	Type    string            `json:"type"`
	Summary string            `json:"summary"`
	Fields  []automationField `json:"fields"`
}

type automationView struct {
	ID          string                    `json:"id"`
	Enabled     bool                      `json:"enabled"`
	Trigger     string                    `json:"trigger"`
	Event       string                    `json:"event,omitempty"`
	Category    string                    `json:"category,omitempty"`
	Conditions  []string                  `json:"conditions"`
	Actions     []automationActionView    `json:"actions"`
	ActionCount int                       `json:"action_count"`
	RateLimit   *cmux.AutomationRateLimit `json:"rate_limit,omitempty"`
	LastRun     *cmux.AutomationLog       `json:"last_run,omitempty"`
}

type automationsResponse struct {
	Rules       []automationView `json:"rules"`
	ConfigError string           `json:"config_error,omitempty"`
}

type automationLogsResponse struct {
	Logs []cmux.AutomationLog `json:"logs"`
}

func automationCall[T any](s *Server, method string, call func() (T, error)) (T, error) {
	return guarded(s, method, errAutomationsUnsupported, call)
}

func (s *Server) automations(ctx context.Context) (automationsResponse, error) {
	rules, err := automationCall(s, cmux.MethodAutomationList, func() ([]cmux.AutomationSummary, error) { return s.cmux.Automations(ctx) })
	if err != nil {
		return automationsResponse{}, err
	}
	logs, _ := probe(s, cmux.MethodAutomationLogs, func() ([]cmux.AutomationLog, error) {
		return s.cmux.AutomationLogs(ctx, automationLogCapacity)
	})
	response := automationsResponse{Rules: make([]automationView, 0, len(rules))}
	for _, summary := range rules {
		response.Rules = append(response.Rules, s.automationView(ctx, summary, logs))
	}
	if len(rules) == 0 {
		if last := lastConfigReload(logs); last != nil && last.Status == "error" && s.describesCurrentFile(*last) {
			response.ConfigError = last.Detail
		}
	}
	return response, nil
}

func (s *Server) automationView(ctx context.Context, summary cmux.AutomationSummary, logs []cmux.AutomationLog) automationView {
	view := summaryView(summary)
	rule, err := automationCall(s, cmux.MethodAutomationShow, func() (cmux.AutomationRule, error) { return s.cmux.Automation(ctx, summary.ID) })
	if err == nil {
		describeRule(&view, rule)
	}
	view.LastRun = lastRun(logs, summary.ID)
	return view
}

func (s *Server) automationLogs(ctx context.Context, id string, limit int) (automationLogsResponse, error) {
	if limit <= 0 {
		limit = defaultAutomationLogs
	}
	limit = min(limit, automationLogCapacity)
	fetch := limit
	if id != "" {
		fetch = automationLogCapacity
	}
	logs, err := automationCall(s, cmux.MethodAutomationLogs, func() ([]cmux.AutomationLog, error) { return s.cmux.AutomationLogs(ctx, fetch) })
	if err != nil {
		return automationLogsResponse{}, err
	}
	newestFirst := make([]cmux.AutomationLog, 0, min(limit, len(logs)))
	for _, entry := range slices.Backward(logs) {
		if len(newestFirst) == limit {
			break
		}
		if id == "" || entry.RuleID == id {
			newestFirst = append(newestFirst, entry)
		}
	}
	return automationLogsResponse{Logs: newestFirst}, nil
}

func (s *Server) setAutomationEnabled(ctx context.Context, id string, enabled bool) (automationView, error) {
	method := cmux.MethodAutomationDisable
	configEvent := "config.disable"
	if enabled {
		method = cmux.MethodAutomationEnable
		configEvent = "config.enable"
	}
	started := time.Now().Truncate(time.Second)
	if _, err := automationCall(s, method, func() (struct{}, error) { return struct{}{}, s.cmux.SetAutomationEnabled(ctx, id, enabled) }); err != nil {
		return automationView{}, err
	}
	deadline := time.Now().Add(automationSettleTimeout)
	for {
		rules, err := s.cmux.Automations(ctx)
		if err != nil {
			return automationView{}, err
		}
		index := slices.IndexFunc(rules, func(rule cmux.AutomationSummary) bool { return rule.ID == id })
		if index < 0 {
			return automationView{}, &requestError{Code: "not_found", Message: "cmux doesn't have the rule " + id + " anymore."}
		}
		logs, _ := probe(s, cmux.MethodAutomationLogs, func() ([]cmux.AutomationLog, error) {
			return s.cmux.AutomationLogs(ctx, automationLogCapacity)
		})
		settled := rules[index].Enabled == enabled
		if !settled {
			if failure := failureSince(logs, id, configEvent, started); failure != nil {
				return automationView{}, &requestError{Code: "automation_failed", Message: "cmux couldn't change the rule: " + failure.Detail}
			}
		}
		if settled || time.Now().After(deadline) || !pause(ctx, automationSettleStep) {
			return s.automationView(ctx, rules[index], logs), nil
		}
	}
}

func (s *Server) reloadAutomations(ctx context.Context) (automationsResponse, error) {
	if _, err := automationCall(s, cmux.MethodAutomationReload, func() (struct{}, error) { return struct{}{}, s.cmux.ReloadAutomations(ctx) }); err != nil {
		return automationsResponse{}, err
	}
	pause(ctx, automationReloadDelay)
	return s.automations(ctx)
}

func pause(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func failureSince(logs []cmux.AutomationLog, id, event string, since time.Time) *cmux.AutomationLog {
	for _, entry := range slices.Backward(logs) {
		if entry.RuleID != id || entry.Event != event {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, entry.OccurredAt)
		if err != nil || at.Before(since) {
			return nil
		}
		if entry.Status == "error" {
			return &entry
		}
		return nil
	}
	return nil
}

func lastRun(logs []cmux.AutomationLog, id string) *cmux.AutomationLog {
	for _, entry := range slices.Backward(logs) {
		if entry.RuleID == id && !strings.HasPrefix(entry.Event, "config.") {
			return &entry
		}
	}
	return nil
}

func defaultAutomationsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cmuxterm", "automations.json")
}

func (s *Server) describesCurrentFile(entry cmux.AutomationLog) bool {
	info, err := os.Stat(s.options.AutomationsFile)
	if err != nil {
		return false
	}
	occurred, err := time.Parse(time.RFC3339, entry.OccurredAt)
	if err != nil {
		return true
	}
	return !occurred.Before(info.ModTime().Add(-time.Second))
}

func lastConfigReload(logs []cmux.AutomationLog) *cmux.AutomationLog {
	for _, entry := range slices.Backward(logs) {
		if entry.RuleID == "" && entry.Event == configReloadEvent {
			return &entry
		}
	}
	return nil
}

func summaryView(summary cmux.AutomationSummary) automationView {
	view := automationView{
		ID:          summary.ID,
		Enabled:     summary.Enabled,
		Conditions:  []string{},
		Actions:     []automationActionView{},
		ActionCount: summary.ActionCount,
		RateLimit:   summary.RateLimit,
	}
	if summary.Event != nil {
		view.Event = *summary.Event
	}
	if summary.Category != nil {
		view.Category = *summary.Category
	}
	view.Trigger = triggerText(view.Event, view.Category)
	return view
}

func describeRule(view *automationView, rule cmux.AutomationRule) {
	view.Event, view.Category = rule.When.Event, rule.When.Category
	view.Trigger = triggerText(view.Event, view.Category)
	keys := make([]string, 0, len(rule.Where))
	for key := range rule.Where {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		view.Conditions = append(view.Conditions, conditionText(key, rule.Where[key]))
	}
	for _, action := range rule.Then {
		view.Actions = append(view.Actions, actionView(action))
	}
	view.ActionCount = len(rule.Then)
	if rule.RateLimit != nil {
		view.RateLimit = rule.RateLimit
	}
}

func triggerText(event, category string) string {
	switch {
	case event != "" && category != "":
		return event + " in " + category
	case event != "":
		return event
	case category != "":
		return "any event in " + category
	default:
		return "any event"
	}
}

func conditionText(key string, raw json.RawMessage) string {
	var operators map[string]json.RawMessage
	if json.Unmarshal(raw, &operators) != nil {
		return key + " is " + valueText(raw)
	}
	names := make([]string, 0, len(operators))
	for name := range operators {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := operators[name]
		switch name {
		case "equals":
			parts = append(parts, key+" is "+valueText(value))
		case "contains":
			parts = append(parts, key+" contains "+valueText(value))
		case "prefix":
			parts = append(parts, key+" starts with "+valueText(value))
		case "suffix":
			parts = append(parts, key+" ends with "+valueText(value))
		case "in":
			parts = append(parts, key+" is one of "+listText(value))
		case "not":
			var nested map[string]json.RawMessage
			if json.Unmarshal(value, &nested) == nil {
				parts = append(parts, "not ("+conditionText(key, value)+")")
			} else {
				parts = append(parts, key+" is not "+valueText(value))
			}
		default:
			parts = append(parts, key+" "+name+" "+valueText(value))
		}
	}
	return strings.Join(parts, " and ")
}

func valueText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return fmt.Sprintf("%q", text)
	}
	return string(raw)
}

func listText(raw json.RawMessage) string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return valueText(raw)
	}
	texts := make([]string, len(items))
	for index, item := range items {
		texts[index] = valueText(item)
	}
	return strings.Join(texts, ", ")
}

var actionFieldOrder = []string{"title", "subtitle", "body", "message", "method", "params", "command", "cmd", "url", "headers"}

func actionView(action map[string]any) automationActionView {
	kind, _ := action["action"].(string)
	text := func(key string) string {
		value, _ := action[key].(string)
		return value
	}
	view := automationActionView{Type: kind, Fields: []automationField{}}
	switch kind {
	case "notify":
		view.Summary = "Notify"
		if title := cmp.Or(text("title"), text("body"), text("message")); title != "" {
			view.Summary = "Notify “" + shorten(title) + "”"
		}
	case "rpc":
		view.Summary = "Call " + cmp.Or(text("method"), "a cmux method")
	case "run":
		view.Summary = "Run " + shorten(cmp.Or(text("command"), text("cmd"), "a command"))
	case "webhook":
		view.Summary = "Send a webhook to " + shorten(cmp.Or(text("url"), "a URL"))
	default:
		view.Summary = cmp.Or(kind, "Unknown action")
	}
	keys := make([]string, 0, len(action))
	for key := range action {
		if key != "action" {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(cmp.Compare(fieldRank(a), fieldRank(b)), cmp.Compare(a, b))
	})
	for _, key := range keys {
		view.Fields = append(view.Fields, automationField{Name: key, Value: fieldText(action[key])})
	}
	return view
}

func fieldRank(key string) int {
	if index := slices.Index(actionFieldOrder, key); index >= 0 {
		return index
	}
	return len(actionFieldOrder)
}

func fieldText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func shorten(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= automationSummaryLength {
		return text
	}
	return string(runes[:automationSummaryLength-1]) + "…"
}
