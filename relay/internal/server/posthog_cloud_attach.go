package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/gantoine/muxer/relay/internal/approvals"
	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

const attachFrameInterval = 200 * time.Millisecond

func (s *Server) AttachPostHogCloud(ctx context.Context, conn net.Conn, taskID, runID string) {
	var writing sync.Mutex
	write := func(frame posthogcloud.AttachFrame) bool {
		writing.Lock()
		defer writing.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		return json.NewEncoder(conn).Encode(frame) == nil
	}
	client, _, ok, err := s.postHogCloudClient()
	if err != nil || !ok {
		write(posthogcloud.AttachFrame{Type: posthogcloud.AttachResult, Error: errPostHogCloudNotConnected.Message})
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sub := s.cloud.subscribe(client, taskID, runID)
	defer s.cloud.unsubscribe(runID, sub)
	go func() {
		defer cancel()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 64<<10), maxInputBytes)
		for scanner.Scan() {
			var action posthogcloud.AttachAction
			if json.Unmarshal(scanner.Bytes(), &action) != nil {
				continue
			}
			go func() {
				actionCtx, cancelAction := context.WithTimeout(ctx, postHogCloudActionTimeout)
				defer cancelAction()
				frame := posthogcloud.AttachFrame{Type: posthogcloud.AttachResult, OK: true}
				if err := s.attachAction(actionCtx, client, sub, taskID, runID, action); err != nil {
					frame.OK, frame.Error = false, attachError(err)
				}
				write(frame)
			}()
		}
	}()
	snapshot := func() posthogcloud.AttachFrame {
		follower := sub.feed.current()
		entries, run, state, ready := follower.Snapshot()
		if len(entries) > maxInitialEntries {
			entries = entries[len(entries)-maxInitialEntries:]
		}
		return posthogcloud.AttachFrame{Type: posthogcloud.AttachSnapshot, Ready: ready, Entries: entries, Status: run.Status, State: state, Pending: follower.Pending()}
	}
	if !write(snapshot()) {
		return
	}
	ticker := time.NewTicker(attachFrameInterval)
	defer ticker.Stop()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.updates:
			dirty = true
		case <-sub.wake:
			dirty = true
		case <-ticker.C:
			if !dirty {
				continue
			}
			dirty = false
			for len(sub.updates) > 0 {
				<-sub.updates
			}
			sub.stale.Store(false)
			if !write(snapshot()) {
				return
			}
		}
	}
}

func (s *Server) attachAction(ctx context.Context, client *posthogcloud.Client, sub *cloudSubscription, taskID, runID string, action posthogcloud.AttachAction) error {
	switch action.Action {
	case posthogcloud.ActionSend:
		return s.cloud.sendMessage(ctx, client, taskID, runID, action.Text)
	case posthogcloud.ActionStop:
		return s.cloud.stopTurn(ctx, client, taskID, runID)
	case posthogcloud.ActionAnswer:
		pending := sub.feed.current().Pending()
		if len(pending) == 0 {
			return errors.New("nothing is waiting for an answer")
		}
		return s.cloud.answer(ctx, postHogCloudApprovalPrefix+runID+":"+pending[0].RequestID, approvals.Answer{Mode: action.Mode})
	}
	return errors.New("unknown action " + action.Action)
}

func attachError(err error) string {
	var request *requestError
	if errors.As(err, &request) {
		return request.Message
	}
	if errors.Is(err, approvals.ErrNotPending) {
		return "That request was already answered."
	}
	var invalidAnswer approvals.InvalidAnswer
	if errors.As(err, &invalidAnswer) {
		return string(invalidAnswer) + " Answer it in the app."
	}
	return err.Error()
}
