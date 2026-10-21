package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/gantoine/phcloud/internal/chat"
	"github.com/gantoine/phcloud/internal/posthogcloud"
)

const (
	dim   = "\x1b[2m"
	bold  = "\x1b[1m"
	cyan  = "\x1b[36m"
	green = "\x1b[32m"
	red   = "\x1b[31m"
	reset = "\x1b[0m"

	maxPrintedOutputLines = 6
)

const attachHelp = "Type a message and press Enter to send it. /approve, /always and /deny answer the agent's question. /stop interrupts it. Ctrl-C closes this view; the run keeps going."

type viewSnapshot struct {
	entries []chat.Entry
	status  string
	state   string
	ready   bool
}

func attach(paths paths, args []string) error {
	flags := flag.NewFlagSet("attach", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return errors.New("usage: phcloud attach TASK RUN")
	}
	config, err := loadConfig(paths)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	source := &directSource{client: posthogcloud.NewClient(config, nil), taskID: flags.Arg(0), runID: flags.Arg(1), restart: make(chan struct{}, 1)}
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
	source *directSource
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

func parseViewerLine(line string) (viewerAction, error) {
	switch line {
	case "/stop":
		return viewerAction{kind: actionStop}, nil
	case "/approve":
		return viewerAction{kind: actionAnswer, mode: answerOnce}, nil
	case "/always":
		return viewerAction{kind: actionAnswer, mode: answerAlways}, nil
	case "/deny":
		return viewerAction{kind: actionAnswer, mode: answerDeny}, nil
	}
	if strings.HasPrefix(line, "/") {
		return viewerAction{}, fmt.Errorf("unknown command %s; /help lists them", line)
	}
	return viewerAction{kind: actionSend, text: line}, nil
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

type viewerAction struct {
	kind string
	text string
	mode string
}

const (
	actionSend   = "send"
	actionStop   = "stop"
	actionAnswer = "answer"
)

func (s *directSource) act(ctx context.Context, action viewerAction) error {
	switch action.kind {
	case actionStop:
		return stopRun(ctx, s.client, s.taskID, s.runID)
	case actionAnswer:
		return answerApproval(ctx, s.client, s.taskID, s.runID, s.current(), action.mode)
	}
	resumed, err := sendMessage(ctx, s.client, s.taskID, s.runID, action.text, s.snapshot().state == posthogcloud.StateWorking)
	if resumed {
		select {
		case s.restart <- struct{}{}:
		default:
		}
	}
	return err
}
