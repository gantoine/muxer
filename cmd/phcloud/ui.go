package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	paddingX     = 2
	paddingY     = 1
	defaultWidth = 40
	minWidth     = 16
)

var (
	accent = lipgloss.Color("12")
	muted  = lipgloss.Color("8")
	alert  = lipgloss.Color("9")
	warn   = lipgloss.Color("11")
	good   = lipgloss.Color("10")

	dimStyle    = lipgloss.NewStyle().Foreground(muted)
	boldStyle   = lipgloss.NewStyle().Bold(true)
	accentStyle = lipgloss.NewStyle().Foreground(accent)
	warnStyle   = lipgloss.NewStyle().Foreground(warn)
	alertStyle  = lipgloss.NewStyle().Foreground(alert)
	goodStyle   = lipgloss.NewStyle().Foreground(good)

	titleStyle    = lipgloss.NewStyle().Bold(true)
	labelStyle    = lipgloss.NewStyle().Foreground(muted).Bold(true)
	focusLabel    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	barStyle      = lipgloss.NewStyle().Foreground(accent)
	pillOn        = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(accent).Bold(true).Padding(0, 1)
	pillOnBlurred = lipgloss.NewStyle().Foreground(accent).Bold(true).Padding(0, 1)
	pillOff       = lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
	keyStyle      = lipgloss.NewStyle().Bold(true)
	cardStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(alert).Padding(0, 1)
)

type keyHint struct{ key, action string }

func contentWidth(width int) int {
	if width == 0 {
		width = defaultWidth
	}
	return max(minWidth, width-2*paddingX)
}

func frame(width, height int, body, footer string) string {
	inner := strings.TrimRight(body, "\n")
	if footer != "" {
		gap := 1
		if height > 0 {
			gap = max(1, height-2*paddingY-lipgloss.Height(inner)-lipgloss.Height(footer))
		}
		inner += strings.Repeat("\n", gap+1) + footer
	}
	return lipgloss.NewStyle().Padding(paddingY, paddingX).Render(inner)
}

func label(text string, focused bool) string {
	if focused {
		return focusLabel.Render(strings.ToUpper(text))
	}
	return labelStyle.Render(strings.ToUpper(text))
}

func pills(options []string, selected int, focused bool, width int) string {
	rendered := make([]string, len(options))
	for i, option := range options {
		switch {
		case i == selected && focused:
			rendered[i] = pillOn.Render(option)
		case i == selected:
			rendered[i] = pillOnBlurred.Render(option)
		default:
			rendered[i] = pillOff.Render(option)
		}
	}
	row := strings.Join(rendered, " ")
	if lipgloss.Width(row) <= width {
		return row
	}
	return strings.Join(rendered, "\n")
}

func hints(keys []keyHint, width int) string {
	var lines []string
	line := ""
	for _, hint := range keys {
		item := keyStyle.Render(hint.key) + " " + dimStyle.Render(hint.action)
		if line != "" && lipgloss.Width(line)+3+lipgloss.Width(item) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += "   "
		}
		line += item
	}
	return strings.Join(append(lines, line), "\n")
}

func wrap(text string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(text)
}

func truncateWidth(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
