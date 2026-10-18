package screen

import (
	"testing"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

func replay(columns, rows int, styles []cmux.Style, spans ...cmux.Span) cmux.Replay {
	return cmux.Replay{
		WorkspaceID: "w",
		SurfaceID:   "s",
		RenderGrid: &cmux.RenderGrid{
			Columns:            columns,
			Rows:               rows,
			Styles:             styles,
			RowSpans:           spans,
			TerminalForeground: "#FFFFFF",
			TerminalBackground: "#000000",
			Cursor:             &cmux.Cursor{Row: 0, Column: 0, Visible: true, Style: "block"},
		},
	}
}

var (
	plain = cmux.Style{ID: 0, Foreground: "#FFFFFF", Background: "#000000"}
	red   = cmux.Style{ID: 1, Foreground: "#FF0000", Background: "#000000", Bold: true}
)

func TestTrackerSendsOnlyChangedRows(t *testing.T) {
	tracker := NewTracker()
	first, changed := tracker.Update(replay(80, 3, []cmux.Style{plain, red},
		cmux.Span{Row: 0, Column: 0, StyleID: 0, Text: "$ ls"},
		cmux.Span{Row: 1, Column: 0, StyleID: 1, Text: "error"},
	))
	if !changed || !first.Full || len(first.Changed) != 2 || len(first.Styles) != 2 {
		t.Fatalf("first frame should be full with both rows and styles: %+v", first)
	}

	_, changed = tracker.Update(replay(80, 3, []cmux.Style{plain, red},
		cmux.Span{Row: 0, Column: 0, StyleID: 0, Text: "$ ls"},
		cmux.Span{Row: 1, Column: 0, StyleID: 1, Text: "error"},
	))
	if changed {
		t.Fatal("an identical screen should not produce a frame")
	}

	renumberedRed, renumberedPlain := red, plain
	renumberedRed.ID, renumberedPlain.ID = 0, 1
	third, changed := tracker.Update(replay(80, 3, []cmux.Style{renumberedRed, renumberedPlain},
		cmux.Span{Row: 0, Column: 0, StyleID: 1, Text: "$ ls"},
		cmux.Span{Row: 1, Column: 0, StyleID: 0, Text: "error"},
		cmux.Span{Row: 2, Column: 0, StyleID: 1, Text: "done"},
	))
	if !changed || third.Full {
		t.Fatalf("expected a partial frame, got %+v", third)
	}
	if len(third.Styles) != 0 {
		t.Fatalf("styles already sent must not be resent when cmux renumbers them: %+v", third.Styles)
	}
	rows := map[int][]Span{}
	for _, row := range third.Changed {
		rows[row.Index] = row.Spans
	}
	if len(rows) != 1 || rows[2] == nil || rows[2][0].Text != "done" {
		t.Fatalf("only row 2 changed after renumbering, got %+v", third.Changed)
	}
}

func TestTrackerReportsClearedRowsAndResizes(t *testing.T) {
	tracker := NewTracker()
	tracker.Update(replay(80, 2, []cmux.Style{plain}, cmux.Span{Row: 1, Column: 0, StyleID: 0, Text: "old"}))

	cleared, _ := tracker.Update(replay(80, 2, []cmux.Style{plain}))
	if len(cleared.Changed) != 1 || cleared.Changed[0].Index != 1 || len(cleared.Changed[0].Spans) != 0 {
		t.Fatalf("a cleared row must be sent with no spans, got %+v", cleared.Changed)
	}

	resized, changed := tracker.Update(replay(100, 4, []cmux.Style{plain}, cmux.Span{Row: 3, Column: 0, StyleID: 0, Text: "x"}))
	if !changed || !resized.Full || resized.Columns != 100 || resized.Rows != 4 {
		t.Fatalf("a resize must produce a full frame, got %+v", resized)
	}
	if len(resized.Styles) == 0 {
		t.Fatal("a full frame must carry the whole style table")
	}
}

func TestTrackerReportsRowsThatScrolledIntoHistory(t *testing.T) {
	tracker := NewTracker()
	first, _ := tracker.Update(replay(80, 2, []cmux.Style{plain}, cmux.Span{Row: 0, Column: 0, StyleID: 0, Text: "$"}))
	if first.HistoryRows != 0 {
		t.Fatalf("a new terminal has no history, got %d", first.HistoryRows)
	}

	scrolled := replay(80, 2, []cmux.Style{plain}, cmux.Span{Row: 0, Column: 0, StyleID: 0, Text: "$"})
	scrolled.RenderGrid.HistoryRows = 40
	frame, changed := tracker.Update(scrolled)
	if !changed || frame.HistoryRows != 40 {
		t.Fatalf("rows scrolling off the top must produce a frame with the history size, got changed=%v %+v", changed, frame)
	}
}

func TestTrackerUsesCellWidthForWideCharacters(t *testing.T) {
	width := 2
	frame, _ := NewTracker().Update(replay(80, 1, []cmux.Style{plain},
		cmux.Span{Row: 0, Column: 5, StyleID: 0, Text: "日", CellWidth: &width},
		cmux.Span{Row: 0, Column: 7, StyleID: 0, Text: "ab"},
	))
	spans := frame.Changed[0].Spans
	if spans[0].Width != 2 || spans[1].Width != 2 || spans[1].Column != 7 {
		t.Fatalf("unexpected span widths: %+v", spans)
	}
}
