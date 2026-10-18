package cmux

import (
	"context"
	"encoding/json"
)

const (
	MethodAutomationList    = "automation.list"
	MethodAutomationShow    = "automation.show"
	MethodAutomationEnable  = "automation.enable"
	MethodAutomationDisable = "automation.disable"
	MethodAutomationLogs    = "automation.logs"
	MethodAutomationReload  = "automation.reload"
)

type AutomationRateLimit struct {
	IntervalSeconds float64 `json:"interval_seconds"`
	Maximum         int     `json:"maximum"`
}

type AutomationSummary struct {
	ID          string               `json:"id"`
	Enabled     bool                 `json:"enabled"`
	Event       *string              `json:"event"`
	Category    *string              `json:"category"`
	ActionCount int                  `json:"action_count"`
	RateLimit   *AutomationRateLimit `json:"rate_limit"`
}

type AutomationWhen struct {
	Event    string `json:"event"`
	Category string `json:"category"`
}

type AutomationRule struct {
	ID        string                     `json:"id"`
	When      AutomationWhen             `json:"when"`
	Where     map[string]json.RawMessage `json:"where"`
	Then      []map[string]any           `json:"then"`
	Enabled   *bool                      `json:"enabled"`
	RateLimit *AutomationRateLimit       `json:"rate_limit"`
}

type AutomationLog struct {
	OccurredAt string   `json:"occurred_at"`
	RuleID     string   `json:"rule_id"`
	Event      string   `json:"event"`
	Status     string   `json:"status"`
	Detail     string   `json:"detail"`
	Chain      []string `json:"chain"`
}

func (c *Client) Automations(ctx context.Context) ([]AutomationSummary, error) {
	var result struct {
		Rules []AutomationSummary `json:"rules"`
	}
	err := c.Call(ctx, MethodAutomationList, nil, &result)
	return result.Rules, err
}

func (c *Client) Automation(ctx context.Context, id string) (AutomationRule, error) {
	var rule AutomationRule
	err := c.Call(ctx, MethodAutomationShow, map[string]any{"id": id}, &rule)
	return rule, err
}

func (c *Client) SetAutomationEnabled(ctx context.Context, id string, enabled bool) error {
	method := MethodAutomationDisable
	if enabled {
		method = MethodAutomationEnable
	}
	return c.Call(ctx, method, map[string]any{"id": id}, nil)
}

func (c *Client) AutomationLogs(ctx context.Context, limit int) ([]AutomationLog, error) {
	var result struct {
		Logs []AutomationLog `json:"logs"`
	}
	err := c.Call(ctx, MethodAutomationLogs, map[string]any{"limit": limit}, &result)
	return result.Logs, err
}

func (c *Client) ReloadAutomations(ctx context.Context) error {
	return c.Call(ctx, MethodAutomationReload, nil, nil)
}
