package posthogcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHost       = "https://us.posthog.com"
	maxLogPage        = 5000
	maxResponseBytes  = 64 << 20
	requestTimeout    = 30 * time.Second
	ignoredLogEntries = "_posthog/console,_posthog/usage_update,_posthog/sandbox_output"
)

var (
	ErrUnauthorized = errors.New("PostHog refused the personal API key")
	ErrForbidden    = errors.New("PostHog refused access to Tasks for this key")
)

type RunSummary struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
	CreatedAt    string `json:"created_at"`
	CompletedAt  string `json:"completed_at"`
}

type Task struct {
	ID            string      `json:"id"`
	Number        int         `json:"task_number"`
	Title         string      `json:"title"`
	Description   string      `json:"description_preview"`
	Repository    string      `json:"repository"`
	OriginProduct string      `json:"origin_product"`
	LatestRun     *RunSummary `json:"latest_run"`
	CreatedAt     string      `json:"created_at"`
	UpdatedAt     string      `json:"updated_at"`
}

type Run struct {
	ID             string         `json:"id"`
	Task           string         `json:"task"`
	Status         string         `json:"status"`
	Stage          string         `json:"stage"`
	Branch         string         `json:"branch"`
	Environment    string         `json:"environment"`
	RuntimeAdapter string         `json:"runtime_adapter"`
	Model          string         `json:"model"`
	ErrorMessage   string         `json:"error_message"`
	Output         map[string]any `json:"output"`
	State          map[string]any `json:"state"`
	Artifacts      []Artifact     `json:"artifacts"`
	CreatedAt      string         `json:"created_at"`
	CompletedAt    string         `json:"completed_at"`
}

func (r Run) ClaudeSubscriptionUser() int {
	value, _ := r.State["claude_subscription_user_id"].(float64)
	return int(value)
}

func (r Run) Terminal() bool {
	return Terminal(r.Status)
}

func (r Run) PullRequestURL() string {
	value, _ := r.Output["pr_url"].(string)
	return value
}

func Terminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

type CommandError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("PostHog refused the command (%d %s): %s", e.Status, e.Code, e.Message)
}

func (e *CommandError) Retryable() bool {
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return e.Code != "usage_limit_reached"
	}
	return false
}

func (e *CommandError) RunEnded() bool {
	return e.Status == http.StatusConflict
}

func (c *Client) Command(ctx context.Context, taskID, runID, method string, params any) (json.RawMessage, error) {
	body := map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": strconv.FormatInt(time.Now().UnixNano(), 36)}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.postJSON(ctx, c.runPath(taskID, runID, "command/"), body, &reply); err != nil {
		return nil, err
	}
	if reply.Error != nil {
		return nil, &CommandError{Status: http.StatusOK, Code: "agent_error", Message: reply.Error.Message}
	}
	return reply.Result, nil
}

const defaultCommandRetry = 2 * time.Second

func (c *Client) CommandWithRetry(ctx context.Context, taskID, runID, method string, params any) (json.RawMessage, error) {
	for {
		result, err := c.Command(ctx, taskID, runID, method, params)
		var failure *CommandError
		if err == nil || !errors.As(err, &failure) || !failure.Retryable() {
			return result, err
		}
		wait := failure.RetryAfter
		if wait <= 0 {
			wait = defaultCommandRetry
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(wait):
		}
	}
}

func (c *Client) ResumeInCloud(ctx context.Context, taskID, runID string) (Run, error) {
	var run Run
	err := c.postJSON(ctx, c.runPath(taskID, runID, "resume_in_cloud/"), map[string]any{}, &run)
	return run, err
}

func (c *Client) postJSON(ctx context.Context, path string, body, into any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.config.Host, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode >= 300 {
		return commandError(response, raw)
	}
	if into == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func commandError(response *http.Response, raw []byte) error {
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	}
	var body struct {
		Detail string `json:"detail"`
		Error  string `json:"error"`
		Code   string `json:"code"`
		Type   string `json:"type"`
	}
	_ = json.Unmarshal(raw, &body)
	failure := &CommandError{
		Status:  response.StatusCode,
		Code:    firstNonEmpty(body.Code, body.Type),
		Message: firstNonEmpty(body.Error, body.Detail, strings.TrimSpace(string(raw[:min(len(raw), 300)]))),
	}
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		failure.RetryAfter = time.Duration(seconds) * time.Second
	}
	return failure
}

type User struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	Team      struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("PostHog answered %d: %s", e.Status, e.Body)
}

type Client struct {
	config Config
	http   *http.Client
}

func NewClient(config Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	return &Client{config: config, http: httpClient}
}

func (c *Client) Me(ctx context.Context) (User, error) {
	var user User
	err := c.getJSON(ctx, "/api/users/@me/", nil, &user)
	return user, err
}

func (c *Client) Tasks(ctx context.Context, limit int) ([]Task, error) {
	query := url.Values{
		"basic":                  {"true"},
		"ordering":               {"-last_activity_at"},
		"limit":                  {strconv.Itoa(limit)},
		"exclude_origin_product": {"signals_scout"},
	}
	if c.config.UserID != 0 {
		query.Set("created_by", strconv.Itoa(c.config.UserID))
	}
	var page struct {
		Results []Task `json:"results"`
	}
	err := c.getJSON(ctx, c.projectPath("tasks/"), query, &page)
	return page.Results, err
}

func (c *Client) Run(ctx context.Context, taskID, runID string) (Run, error) {
	var run Run
	err := c.getJSON(ctx, c.runPath(taskID, runID, ""), nil, &run)
	return run, err
}

func (c *Client) SessionLogs(ctx context.Context, taskID, runID string, offset int) ([]json.RawMessage, bool, error) {
	query := url.Values{
		"offset":        {strconv.Itoa(offset)},
		"limit":         {strconv.Itoa(maxLogPage)},
		"exclude_types": {ignoredLogEntries},
	}
	response, err := c.get(ctx, c.runPath(taskID, runID, "session_logs/"), query)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	var entries []json.RawMessage
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&entries); err != nil {
		return nil, false, fmt.Errorf("read the run log: %w", err)
	}
	return entries, response.Header.Get("X-Has-More") == "true", nil
}

type StreamToken struct {
	Token   string `json:"token"`
	BaseURL string `json:"stream_base_url"`
}

func (t StreamToken) Leg() string {
	if t.BaseURL != "" && t.Token != "" {
		return "proxy"
	}
	return "django"
}

func (c *Client) StreamToken(ctx context.Context, taskID, runID string) (StreamToken, error) {
	var token StreamToken
	err := c.getJSON(ctx, c.runPath(taskID, runID, "stream_token/"), url.Values{"resync": {"1"}}, &token)
	return token, err
}

type Stream struct {
	body   io.ReadCloser
	events *EventReader
}

func (s *Stream) Next() (Event, error) {
	return s.events.Next()
}

func (s *Stream) Close() error {
	return s.body.Close()
}

func (c *Client) OpenStream(ctx context.Context, taskID, runID string, token StreamToken, lastEventID string) (*Stream, error) {
	var target string
	query := url.Values{}
	if lastEventID == "" {
		query.Set("start", "latest")
	}
	authorization := "Bearer " + c.config.APIKey
	if token.Leg() == "proxy" {
		target = strings.TrimSuffix(token.BaseURL, "/") + "/v1/runs/" + url.PathEscape(runID) + "/stream"
		query.Set("resync", "1")
		authorization = "Bearer " + token.Token
	} else {
		target = strings.TrimSuffix(c.config.Host, "/") + c.runPath(taskID, runID, "stream/")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Accept", "text/event-stream")
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := (&http.Client{Transport: c.http.Transport}).Do(request)
	if err != nil {
		return nil, err
	}
	if err := responseError(response); err != nil {
		return nil, err
	}
	return &Stream{body: response.Body, events: NewEventReader(response.Body)}, nil
}

func (c *Client) runPath(taskID, runID, rest string) string {
	return c.projectPath("tasks/" + url.PathEscape(taskID) + "/runs/" + url.PathEscape(runID) + "/" + rest)
}

func (c *Client) projectPath(rest string) string {
	return "/api/projects/" + strconv.Itoa(c.config.ProjectID) + "/" + rest
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, into any) error {
	response, err := c.get(ctx, path, query)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(into)
}

func (c *Client) get(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	target := strings.TrimSuffix(c.config.Host, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	if err := responseError(response); err != nil {
		return nil, err
	}
	return response, nil
}

func responseError(response *http.Response) error {
	if response.StatusCode < 300 {
		return nil
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, strings.TrimSpace(string(body)))
	}
	return &HTTPError{Status: response.StatusCode, Body: strings.TrimSpace(string(body))}
}
