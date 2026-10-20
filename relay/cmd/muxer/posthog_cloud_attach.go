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

	"github.com/gantoine/muxer/relay/internal/chat"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
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
	config, ok, err := posthogcloud.LoadConfig(paths.postHogCloud)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("PostHog Cloud isn't connected; run `muxer posthog-cloud connect`")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	viewer := &runViewer{
		client:  posthogcloud.NewClient(config, nil),
		taskID:  *taskID,
		runID:   *runID,
		out:     os.Stdout,
		printed: map[string]printedEntry{},
		restart: make(chan struct{}, 1),
	}
	fmt.Fprintln(viewer.out, dim+attachHelp+reset)
	go viewer.readInput(ctx, os.Stdin)
	viewer.follow(ctx)
	return nil
}

type printedEntry struct {
	text   string
	status string
}

type runViewer struct {
	client *posthogcloud.Client
	taskID string
	runID  string
	out    io.Writer

	mu        sync.Mutex
	follower  *posthogcloud.Follower
	printed   map[string]printedEntry
	lastState string
	lastRun   string
	restart   chan struct{}
}

func (v *runViewer) follow(ctx context.Context) {
	for {
		follower := posthogcloud.NewFollower(v.client, v.taskID, v.runID, posthogcloud.FollowerOptions{}, func(update posthogcloud.Update) {
			if update.Err != nil {
				v.note(red + "PostHog: " + update.Err.Error() + reset)
				return
			}
			if update.Credential == nil {
				v.render()
			}
		})
		v.mu.Lock()
		v.follower = follower
		v.mu.Unlock()
		if err := follower.Run(ctx); err != nil && ctx.Err() != nil {
			return
		}
		v.render()
		v.note(dim + "The run has ended. Send a message to bring it back." + reset)
		select {
		case <-ctx.Done():
			return
		case <-v.restart:
		}
	}
}

func (v *runViewer) note(line string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	fmt.Fprintln(v.out, line)
}

func (v *runViewer) render() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.follower == nil {
		return
	}
	entries, run, state, ready := v.follower.Snapshot()
	if !ready {
		return
	}
	for i, entry := range entries {
		growing := i == len(entries)-1 && state == posthogcloud.StateWorking && (entry.Kind == chat.KindAssistant || entry.Kind == chat.KindThinking)
		if growing {
			break
		}
		v.print(entry)
	}
	if run.Status != v.lastRun || state != v.lastState {
		v.lastRun, v.lastState = run.Status, state
		if line := statusLine(run.Status, state); line != "" {
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
			fmt.Fprintln(v.out, mark+" "+dim+outputPreview(tool.Output)+reset)
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
	lines := strings.Split(strings.TrimSpace(output), "\n")
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
		if err := v.act(ctx, line); err != nil {
			v.note(red + err.Error() + reset)
		}
	}
}

func (v *runViewer) act(ctx context.Context, line string) error {
	v.mu.Lock()
	follower := v.follower
	v.mu.Unlock()
	switch line {
	case "/help":
		v.note(dim + attachHelp + reset)
		return nil
	case "/stop":
		_, err := v.client.CommandWithRetry(ctx, v.taskID, v.runID, "cancel", map[string]any{})
		return err
	case "/approve", "/always", "/deny":
		if follower == nil {
			return errors.New("nothing is waiting for an answer")
		}
		pending := follower.Pending()
		if len(pending) == 0 {
			return errors.New("nothing is waiting for an answer")
		}
		request := pending[0]
		kinds := map[string][]string{"/approve": {"allow_once", "allow_always"}, "/always": {"allow_always", "allow_once"}, "/deny": {"reject_once", "reject_always"}}[line]
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
		if _, err := v.client.CommandWithRetry(ctx, v.taskID, v.runID, "permission_response", map[string]any{"requestId": request.RequestID, "optionId": optionID}); err != nil {
			return err
		}
		follower.Resolve(request.RequestID, optionID)
		return nil
	}
	if strings.HasPrefix(line, "/") {
		return fmt.Errorf("unknown command %s; /help lists them", line)
	}
	return v.send(ctx, line)
}

func (v *runViewer) send(ctx context.Context, text string) error {
	run, err := v.client.Run(ctx, v.taskID, v.runID)
	if err != nil {
		return err
	}
	params := map[string]any{"content": text}
	if run.Terminal() {
		v.note(dim + "Reopening the run…" + reset)
		if _, err := v.client.ResumeInCloud(ctx, v.taskID, v.runID); err != nil {
			return fmt.Errorf("PostHog couldn't reopen this run: %w", err)
		}
		select {
		case v.restart <- struct{}{}:
		default:
		}
	} else {
		v.mu.Lock()
		working := v.lastState == posthogcloud.StateWorking
		v.mu.Unlock()
		if working {
			params["steer"] = true
		}
	}
	_, err = v.client.CommandWithRetry(ctx, v.taskID, v.runID, "user_message", params)
	return err
}
