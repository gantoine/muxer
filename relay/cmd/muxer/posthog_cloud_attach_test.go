package main

import (
	"testing"

	"github.com/gantoine/muxer/relay/internal/posthogcloud"
)

func TestViewerLinesBecomeActions(t *testing.T) {
	cases := map[string]posthogcloud.AttachAction{
		"/stop":         {Action: posthogcloud.ActionStop},
		"/approve":      {Action: posthogcloud.ActionAnswer, Mode: "once"},
		"/always":       {Action: posthogcloud.ActionAnswer, Mode: "always"},
		"/deny":         {Action: posthogcloud.ActionAnswer, Mode: "deny"},
		"yes, go ahead": {Action: posthogcloud.ActionSend, Text: "yes, go ahead"},
	}
	for line, want := range cases {
		got, err := parseViewerLine(line)
		if err != nil || got != want {
			t.Errorf("parseViewerLine(%q) = %+v, %v", line, got, err)
		}
	}
	if _, err := parseViewerLine("/nope"); err == nil {
		t.Error("an unknown command must be refused")
	}
	if outputPreview("  \n ") != "" {
		t.Error("an empty tool output prints nothing")
	}
}
