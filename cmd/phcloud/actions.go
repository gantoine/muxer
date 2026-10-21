package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gantoine/phcloud/internal/chat"
	"github.com/gantoine/phcloud/internal/posthogcloud"
)

const (
	answerOnce   = "once"
	answerAlways = "always"
	answerDeny   = "deny"
	maxTitle     = 80
)

func answerApproval(ctx context.Context, client *posthogcloud.Client, taskID, runID string, follower *posthogcloud.Follower, mode string) error {
	if follower == nil || len(follower.Pending()) == 0 {
		return errors.New("nothing is waiting for an answer")
	}
	request := follower.Pending()[0]
	kinds := map[string][]string{answerOnce: {"allow_once", "allow_always"}, answerAlways: {"allow_always", "allow_once"}, answerDeny: {"reject_once", "reject_always"}}[mode]
	optionID := ""
	for _, kind := range kinds {
		for _, option := range request.Options {
			if optionID == "" && option.Kind == kind {
				optionID = option.OptionID
			}
		}
	}
	if optionID == "" {
		return errors.New("the agent offered no matching choice; open the run in PostHog to answer it")
	}
	if _, err := client.CommandWithRetry(ctx, taskID, runID, "permission_response", map[string]any{"requestId": request.RequestID, "optionId": optionID}); err != nil {
		return err
	}
	follower.Resolve(request.RequestID, optionID)
	return nil
}

func stopRun(ctx context.Context, client *posthogcloud.Client, taskID, runID string) error {
	_, err := client.CommandWithRetry(ctx, taskID, runID, "cancel", map[string]any{})
	return err
}

func sendMessage(ctx context.Context, client *posthogcloud.Client, taskID, runID, text string, working bool) (resumed bool, err error) {
	run, err := client.Run(ctx, taskID, runID)
	if err != nil {
		return false, err
	}
	params := map[string]any{"content": text}
	if run.Terminal() {
		if _, err := client.ResumeInCloud(ctx, taskID, runID); err != nil {
			return false, fmt.Errorf("PostHog couldn't reopen this run: %w", err)
		}
		resumed = true
	} else if working {
		params["steer"] = true
	}
	_, err = client.CommandWithRetry(ctx, taskID, runID, "user_message", params)
	return resumed, err
}

func taskTitle(task posthogcloud.Task) string {
	first, _, _ := strings.Cut(strings.TrimSpace(task.Description), "\n")
	return chat.Truncate(cmp.Or(strings.TrimSpace(task.Title), strings.TrimSpace(first), "Cloud run"), maxTitle)
}

func approvalSummary(approval posthogcloud.PendingApproval) string {
	switch approval.Kind {
	case posthogcloud.ApprovalPlan:
		return "approve the plan"
	case posthogcloud.ApprovalQuestion:
		if len(approval.Questions) > 0 {
			return chat.Truncate(approval.Questions[0].Question, maxTitle)
		}
		return "answer a question"
	}
	summary := strings.TrimSpace(cmp.Or(approval.Title, approval.Tool) + " " + approval.Detail)
	first, _, _ := strings.Cut(summary, "\n")
	return chat.Truncate(cmp.Or(first, "allow a tool"), maxTitle)
}
