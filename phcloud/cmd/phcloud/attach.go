package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gantoine/muxer/phcloud/internal/chat"
	"github.com/gantoine/muxer/phcloud/internal/posthogcloud"
)

const viewerThrottle = 80 * time.Millisecond

type viewSnapshot struct {
	entries []chat.Entry
	run     posthogcloud.Run
	state   string
	ready   bool
	pending []posthogcloud.PendingApproval
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := posthogcloud.NewClient(config, nil)
	source := &directSource{client: client, taskID: flags.Arg(0), runID: flags.Arg(1), restart: make(chan struct{}, 1)}
	view := newRunView(ctx, client, source, config, lipgloss.HasDarkBackground())
	program := tea.NewProgram(view, tea.WithAltScreen(), tea.WithMouseCellMotion())
	var pending sync.Mutex
	scheduled := false
	changed := func() {
		pending.Lock()
		defer pending.Unlock()
		if scheduled {
			return
		}
		scheduled = true
		time.AfterFunc(viewerThrottle, func() {
			pending.Lock()
			scheduled = false
			pending.Unlock()
			program.Send(changedMsg{})
		})
	}
	go func() {
		_ = source.run(ctx, changed, func(text string) { program.Send(noticeMsg{text}) })
	}()
	go func() {
		taskCtx, taskCancel := context.WithTimeout(ctx, actionTimeout)
		defer taskCancel()
		if task, err := client.Task(taskCtx, source.taskID); err == nil {
			program.Send(taskMsg{task})
		}
	}()
	_, err = program.Run()
	return err
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
		return viewerAction{}, fmt.Errorf("unknown command %s; try /stop, /approve, /always or /deny", line)
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
	return viewSnapshot{entries: entries, run: run, state: state, ready: ready, pending: follower.Pending()}
}

func (s *directSource) run(ctx context.Context, changed func(), note func(string)) error {
	for {
		follower := posthogcloud.NewFollower(s.client, s.taskID, s.runID, posthogcloud.FollowerOptions{}, func(update posthogcloud.Update) {
			if update.Err != nil {
				note("PostHog: " + update.Err.Error())
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
		select {
		case <-ctx.Done():
			return nil
		case <-s.restart:
		}
	}
}

type viewerAction struct {
	kind   string
	text   string
	mode   string
	option string
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
		if action.option != "" {
			return answerOption(ctx, s.client, s.taskID, s.runID, s.current(), action.option)
		}
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
