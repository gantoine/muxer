package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
	"github.com/gantoine/muxer/relay/internal/server"
)

const (
	dim   = "\x1b[2m"
	bold  = "\x1b[1m"
	cyan  = "\x1b[36m"
	green = "\x1b[32m"
	red   = "\x1b[31m"
	reset = "\x1b[0m"

	maxPrintedOutputLines = 6
	maxAttachFrameBytes   = 64 << 20
)

const attachHelp = "Type a message and press Enter to send it. /approve, /always and /deny answer the agent's question. /stop interrupts it. Ctrl-C closes this view; the run keeps going."

type viewSnapshot struct {
	entries []chat.Entry
	status  string
	state   string
	ready   bool
}

type viewSource interface {
	snapshot() viewSnapshot
	act(ctx context.Context, action posthogcloud.AttachAction) error
	run(ctx context.Context, changed func(), note func(string)) error
}

func attachPostHogCloudRun(paths paths, args []string) error {
	flags := flag.NewFlagSet("posthog-cloud attach", flag.ContinueOnError)
	taskID := flags.String("task", "", "the task id")
	runID := flags.String("run", "", "the run id")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *taskID == "" || *runID == "" {
		return errors.New("usage: muxer posthog-cloud attach --task ID --run ID")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var source viewSource
	relayed, err := relaySourceFor(paths, *taskID, *runID)
	if err == nil {
		source = relayed
	} else {
		config, ok, loadErr := posthogcloud.LoadConfig(paths.postHogCloud)
		if loadErr != nil {
			return loadErr
		}
		if !ok {
			return errors.New("PostHog Cloud isn't connected; run `muxer posthog-cloud connect`")
		}
		source = &directSource{client: posthogcloud.NewClient(config, nil), taskID: *taskID, runID: *runID, restart: make(chan struct{}, 1)}
	}
	viewer := &runViewer{source: source, out: os.Stdout, printed: map[string]printedEntry{}}
	fmt.Fprintln(viewer.out, dim+attachHelp+reset)
	go viewer.readInput(ctx, os.Stdin)
	if err := source.run(ctx, viewer.render, viewer.note); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

type printedEntry struct {
	text   string
	status string
}

type runViewer struct {
	source viewSource
	out    io.Writer

	mu        sync.Mutex
	printed   map[string]printedEntry
	lastState string
	lastRun   string
}

func (v *runViewer) note(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	fmt.Fprintln(v.out, line)
}

func (v *runViewer) render() {
	view := v.source.snapshot()
	v.mu.Lock()
	defer v.mu.Unlock()
	if !view.ready {
		return
	}
	for i, entry := range view.entries {
		growing := i == len(view.entries)-1 && view.state == posthogcloud.StateWorking && (entry.Kind == chat.KindAssistant || entry.Kind == chat.KindThinking)
		if growing {
			break
		}
		v.print(entry)
	}
	if view.status != v.lastRun || view.state != v.lastState {
		v.lastRun, v.lastState = view.status, view.state
		if line := statusLine(view.status, view.state); line != "" {
			fmt.Fprintln(v.out, dim+"— "+line+reset)
		}
	}
}

func (v *runViewer) print(entry chat.Entry) {
	previous, seen := v.printed[entry.ID]
	switch entry.Kind {
	case chat.KindUser:
		if !seen {
			fmt.Fprintln(v.out, "\n"+bold+cyan+"› "+entry.Text+reset)
		}
	case chat.KindAssistant:
		if !seen {
			fmt.Fprintln(v.out, "\n"+entry.Text)
		}
	case chat.KindThinking:
		if !seen {
			fmt.Fprintln(v.out, dim+"thinking: "+firstLine(entry.Text)+reset)
		}
	case chat.KindNotice:
		if !seen || previous.text != entry.Text {
			fmt.Fprintln(v.out, dim+"· "+entry.Text+reset)
		}
	case chat.KindTool:
		tool := entry.Tool
		if tool == nil {
			return
		}
		if !seen {
			fmt.Fprintln(v.out, dim+"▸ "+reset+tool.Name+" "+dim+tool.Summary+reset)
		}
		if tool.Status != previous.status && tool.Status != chat.ToolRunning {
			mark := green + "  ✓" + reset
			if tool.Status == chat.ToolFailed {
				mark = red + "  ✗" + reset
			}
			if preview := outputPreview(tool.Output); preview != "" {
				mark += " " + dim + preview + reset
			}
			fmt.Fprintln(v.out, mark)
		}
		v.printed[entry.ID] = printedEntry{text: entry.Text, status: tool.Status}
		return
	}
	v.printed[entry.ID] = printedEntry{text: entry.Text}
}

func statusLine(status, state string) string {
	switch status {
	case "completed":
		return "Done."
	case "failed":
		return "The run failed."
	case "cancelled":
		return "The run was stopped."
	}
	switch state {
	case posthogcloud.StateWaiting:
		return "Waiting for your approval: /approve, /always or /deny."
	case posthogcloud.StateIdle:
		return "Your turn."
	}
	return ""
}

func outputPreview(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if len(lines) > maxPrintedOutputLines {
		lines = append(lines[:maxPrintedOutputLines], fmt.Sprintf("… %d more lines", len(lines)-maxPrintedOutputLines))
	}
	return strings.Join(lines, "\n    ")
}

func firstLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return chat.Truncate(first, chat.MaxSummaryLength)
}

func (v *runViewer) readInput(ctx context.Context, input io.Reader) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/help" {
			v.note(dim + attachHelp + reset)
			continue
		}
		action, err := parseViewerLine(line)
		if err == nil {
			err = v.source.act(ctx, action)
		}
		if err != nil {
			v.note(red + err.Error() + reset)
		}
	}
}

func parseViewerLine(line string) (posthogcloud.AttachAction, error) {
	switch line {
	case "/stop":
		return posthogcloud.AttachAction{Action: posthogcloud.ActionStop}, nil
	case "/approve":
		return posthogcloud.AttachAction{Action: posthogcloud.ActionAnswer, Mode: "once"}, nil
	case "/always":
		return posthogcloud.AttachAction{Action: posthogcloud.ActionAnswer, Mode: "always"}, nil
	case "/deny":
		return posthogcloud.AttachAction{Action: posthogcloud.ActionAnswer, Mode: "deny"}, nil
	}
	if strings.HasPrefix(line, "/") {
		return posthogcloud.AttachAction{}, fmt.Errorf("unknown command %s; /help lists them", line)
	}
	return posthogcloud.AttachAction{Action: posthogcloud.ActionSend, Text: line}, nil
}

type relaySource struct {
	conn    net.Conn
	frames  *bufio.Scanner
	acting  sync.Mutex
	results chan posthogcloud.AttachFrame

	mu     sync.Mutex
	latest viewSnapshot
}

func relaySourceFor(paths paths, taskID, runID string) (*relaySource, error) {
	conn, err := server.DialAdmin(paths.admin, server.AdminRequest{Command: "posthog_cloud_attach", TaskID: taskID, RunID: runID})
	if err != nil {
		return nil, err
	}
	frames := bufio.NewScanner(conn)
	frames.Buffer(make([]byte, 64<<10), maxAttachFrameBytes)
	if !frames.Scan() {
		conn.Close()
		return nil, errors.New("the relay closed the attach session")
	}
	var first posthogcloud.AttachFrame
	if err := json.Unmarshal(frames.Bytes(), &first); err != nil || first.Type != posthogcloud.AttachSnapshot {
		conn.Close()
		return nil, errors.New("the relay can't follow this run")
	}
	source := &relaySource{conn: conn, frames: frames, results: make(chan posthogcloud.AttachFrame, 1)}
	source.apply(first)
	return source, nil
}

func (s *relaySource) apply(frame posthogcloud.AttachFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = viewSnapshot{entries: frame.Entries, status: frame.Status, state: frame.State, ready: frame.Ready}
}

func (s *relaySource) snapshot() viewSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

func (s *relaySource) run(ctx context.Context, changed func(), note func(string)) error {
	context.AfterFunc(ctx, func() { s.conn.Close() })
	changed()
	for s.frames.Scan() {
		var frame posthogcloud.AttachFrame
		if json.Unmarshal(s.frames.Bytes(), &frame) != nil {
			continue
		}
		switch frame.Type {
		case posthogcloud.AttachSnapshot:
			s.apply(frame)
			changed()
		case posthogcloud.AttachResult:
			select {
			case s.results <- frame:
			default:
			}
		}
	}
	if ctx.Err() == nil {
		note(red + "The relay stopped. Run this command again once it's back." + reset)
	}
	return nil
}

func (s *relaySource) act(ctx context.Context, action posthogcloud.AttachAction) error {
	s.acting.Lock()
	defer s.acting.Unlock()
	encoded, err := json.Marshal(action)
	if err != nil {
		return err
	}
	if _, err := s.conn.Write(append(encoded, '\n')); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-s.results:
		if !result.OK {
			return errors.New(result.Error)
		}
		return nil
	}
}

type directSource struct {
	client  *posthogcloud.Client
	taskID  string
	runID   string
	restart chan struct{}

	mu       sync.Mutex
	follower *posthogcloud.Follower
}

func (s *directSource) current() *posthogcloud.Follower {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.follower
}

func (s *directSource) snapshot() viewSnapshot {
	follower := s.current()
	if follower == nil {
		return viewSnapshot{}
	}
	entries, run, state, ready := follower.Snapshot()
	return viewSnapshot{entries: entries, status: run.Status, state: state, ready: ready}
}

func (s *directSource) run(ctx context.Context, changed func(), note func(string)) error {
	for {
		follower := posthogcloud.NewFollower(s.client, s.taskID, s.runID, posthogcloud.FollowerOptions{}, func(update posthogcloud.Update) {
			if update.Err != nil {
				note(red + "PostHog: " + update.Err.Error() + reset)
				return
			}
			if update.Credential == nil {
				changed()
			}
		})
		s.mu.Lock()
		s.follower = follower
		s.mu.Unlock()
		if err := follower.Run(ctx); err != nil && ctx.Err() != nil {
			return nil
		}
		changed()
		note(dim + "The run has ended. Send a message to bring it back." + reset)
		select {
		case <-ctx.Done():
			return nil
		case <-s.restart:
		}
	}
}

func (s *directSource) act(ctx context.Context, action posthogcloud.AttachAction) error {
	switch action.Action {
	case posthogcloud.ActionStop:
		_, err := s.client.CommandWithRetry(ctx, s.taskID, s.runID, "cancel", map[string]any{})
		return err
	case posthogcloud.ActionAnswer:
		return s.answer(ctx, action.Mode)
	}
	return s.send(ctx, action.Text)
}

func (s *directSource) answer(ctx context.Context, mode string) error {
	follower := s.current()
	if follower == nil || len(follower.Pending()) == 0 {
		return errors.New("nothing is waiting for an answer")
	}
	request := follower.Pending()[0]
	kinds := map[string][]string{"once": {"allow_once", "allow_always"}, "always": {"allow_always", "allow_once"}, "deny": {"reject_once", "reject_always"}}[mode]
	optionID := ""
	for _, kind := range kinds {
		for _, option := range request.Options {
			if optionID == "" && option.Kind == kind {
				optionID = option.OptionID
			}
		}
	}
	if optionID == "" {
		return errors.New("the agent offered no matching choice; answer it in the app")
	}
	if _, err := s.client.CommandWithRetry(ctx, s.taskID, s.runID, "permission_response", map[string]any{"requestId": request.RequestID, "optionId": optionID}); err != nil {
		return err
	}
	follower.Resolve(request.RequestID, optionID)
	return nil
}

func (s *directSource) send(ctx context.Context, text string) error {
	run, err := s.client.Run(ctx, s.taskID, s.runID)
	if err != nil {
		return err
	}
	params := map[string]any{"content": text}
	if run.Terminal() {
		if _, err := s.client.ResumeInCloud(ctx, s.taskID, s.runID); err != nil {
			return fmt.Errorf("PostHog couldn't reopen this run: %w", err)
		}
		select {
		case s.restart <- struct{}{}:
		default:
		}
	} else if s.snapshot().state == posthogcloud.StateWorking {
		params["steer"] = true
	}
	_, err = s.client.CommandWithRetry(ctx, s.taskID, s.runID, "user_message", params)
	return err
}
