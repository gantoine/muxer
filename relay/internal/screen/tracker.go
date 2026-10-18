package screen

import (
	"strconv"
	"unicode/utf8"

	"github.com/gantoine/muxer/relay/internal/cmux"
)

type Style struct {
	ID            int    `json:"id"`
	Foreground    string `json:"fg"`
	Background    string `json:"bg"`
	Bold          bool   `json:"bold,omitempty"`
	Faint         bool   `json:"faint,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	Inverse       bool   `json:"inverse,omitempty"`
	Invisible     bool   `json:"invisible,omitempty"`
}

type Span struct {
	Column int    `json:"column"`
	Width  int    `json:"width"`
	Style  int    `json:"style"`
	Text   string `json:"text"`
}

type Row struct {
	Index int    `json:"row"`
	Spans []Span `json:"spans"`
}

type Cursor struct {
	Row     int    `json:"row"`
	Column  int    `json:"column"`
	Visible bool   `json:"visible"`
	Shape   string `json:"shape"`
}

type Frame struct {
	Type        string  `json:"type"`
	WorkspaceID string  `json:"workspace_id"`
	SurfaceID   string  `json:"surface_id"`
	Full        bool    `json:"full"`
	Columns     int     `json:"columns"`
	Rows        int     `json:"rows"`
	Foreground  string  `json:"foreground"`
	Background  string  `json:"background"`
	AltScreen   bool    `json:"alt_screen"`
	Cursor      *Cursor `json:"cursor,omitempty"`
	Styles      []Style `json:"styles,omitempty"`
	Changed     []Row   `json:"changed"`
}

type styleKey struct {
	foreground, background string
	flags                  uint8
}

type Tracker struct {
	styleIDs   map[styleKey]int
	columns    int
	rows       int
	rowHashes  []uint64
	foreground string
	background string
	altScreen  bool
	cursor     Cursor
	started    bool
}

func NewTracker() *Tracker {
	return &Tracker{styleIDs: map[styleKey]int{}}
}

func (t *Tracker) Reset() {
	t.started = false
}

func (t *Tracker) Update(replay cmux.Replay) (Frame, bool) {
	grid := replay.RenderGrid
	frame := Frame{
		Type:        "screen",
		WorkspaceID: replay.WorkspaceID,
		SurfaceID:   replay.SurfaceID,
		Columns:     grid.Columns,
		Rows:        grid.Rows,
		Foreground:  grid.TerminalForeground,
		Background:  grid.TerminalBackground,
		AltScreen:   isAltScreen(grid),
		Changed:     []Row{},
	}

	full := !t.started || grid.Columns != t.columns || grid.Rows != t.rows ||
		grid.TerminalForeground != t.foreground || grid.TerminalBackground != t.background
	if full {
		t.columns, t.rows = grid.Columns, grid.Rows
		t.rowHashes = make([]uint64, grid.Rows)
		t.foreground, t.background = grid.TerminalForeground, grid.TerminalBackground
		t.styleIDs = map[styleKey]int{}
		t.started = true
	}
	frame.Full = full

	sourceStyles := stylesByID(grid.Styles)

	rows := make([][]Span, grid.Rows)
	for _, span := range grid.RowSpans {
		if span.Row < 0 || span.Row >= grid.Rows {
			continue
		}
		styleID, isNew := t.internStyle(sourceStyles[span.StyleID])
		if isNew {
			frame.Styles = append(frame.Styles, wireStyle(styleID, sourceStyles[span.StyleID]))
		}
		rows[span.Row] = append(rows[span.Row], Span{Column: span.Column, Width: spanWidth(span), Style: styleID, Text: span.Text})
	}
	if full {
		frame.Styles = t.allStyles(sourceStyles, frame.Styles)
	}

	for index, spans := range rows {
		hash := hashRow(spans)
		if full || hash != t.rowHashes[index] {
			t.rowHashes[index] = hash
			if spans == nil {
				spans = []Span{}
			}
			if full && len(spans) == 0 {
				continue
			}
			frame.Changed = append(frame.Changed, Row{Index: index, Spans: spans})
		}
	}

	cursor := Cursor{Row: -1, Column: -1}
	if grid.Cursor != nil {
		cursor = Cursor{Row: grid.Cursor.Row, Column: grid.Cursor.Column, Visible: grid.Cursor.Visible, Shape: grid.Cursor.Style}
	}
	frame.Cursor = &cursor
	changed := full || len(frame.Changed) > 0 || len(frame.Styles) > 0 || cursor != t.cursor || frame.AltScreen != t.altScreen
	t.cursor = cursor
	t.altScreen = frame.AltScreen
	return frame, changed
}

func isAltScreen(grid *cmux.RenderGrid) bool {
	return grid.ActiveScreen == "alternate"
}

func stylesByID(styles []cmux.Style) map[int]cmux.Style {
	byID := make(map[int]cmux.Style, len(styles))
	for _, style := range styles {
		byID[style.ID] = style
	}
	return byID
}

func spanWidth(span cmux.Span) int {
	if span.CellWidth != nil {
		return *span.CellWidth
	}
	return utf8.RuneCountInString(span.Text)
}

func (t *Tracker) internStyle(style cmux.Style) (int, bool) {
	key := keyOf(style)
	if id, ok := t.styleIDs[key]; ok {
		return id, false
	}
	id := len(t.styleIDs)
	t.styleIDs[key] = id
	return id, true
}

func (t *Tracker) allStyles(sourceStyles map[int]cmux.Style, alreadyIncluded []Style) []Style {
	included := make(map[int]bool, len(alreadyIncluded))
	for _, style := range alreadyIncluded {
		included[style.ID] = true
	}
	styles := alreadyIncluded
	for _, style := range sourceStyles {
		id, _ := t.internStyle(style)
		if !included[id] {
			included[id] = true
			styles = append(styles, wireStyle(id, style))
		}
	}
	return styles
}

const (
	flagBold = 1 << iota
	flagFaint
	flagItalic
	flagUnderline
	flagStrikethrough
	flagInverse
	flagInvisible
)

func keyOf(style cmux.Style) styleKey {
	var flags uint8
	for _, attribute := range [...]struct {
		on   bool
		flag uint8
	}{
		{style.Bold, flagBold}, {style.Faint, flagFaint}, {style.Italic, flagItalic}, {style.Underline, flagUnderline},
		{style.Strikethrough, flagStrikethrough}, {style.Inverse, flagInverse}, {style.Invisible, flagInvisible},
	} {
		if attribute.on {
			flags |= attribute.flag
		}
	}
	return styleKey{foreground: style.Foreground, background: style.Background, flags: flags}
}

func wireStyle(id int, style cmux.Style) Style {
	return styleFromKey(id, keyOf(style))
}

func styleFromKey(id int, key styleKey) Style {
	return Style{
		ID:            id,
		Foreground:    key.foreground,
		Background:    key.background,
		Bold:          key.flags&flagBold != 0,
		Faint:         key.flags&flagFaint != 0,
		Italic:        key.flags&flagItalic != 0,
		Underline:     key.flags&flagUnderline != 0,
		Strikethrough: key.flags&flagStrikethrough != 0,
		Inverse:       key.flags&flagInverse != 0,
		Invisible:     key.flags&flagInvisible != 0,
	}
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

type rowHash uint64

func (h *rowHash) writeByte(value byte) {
	*h ^= rowHash(value)
	*h *= fnvPrime64
}

func (h *rowHash) writeString(text string) {
	for index := 0; index < len(text); index++ {
		h.writeByte(text[index])
	}
}

func (h *rowHash) writeInt(value int) {
	var digits [20]byte
	for _, digit := range strconv.AppendInt(digits[:0], int64(value), 10) {
		h.writeByte(digit)
	}
}

func hashRow(spans []Span) uint64 {
	hash := rowHash(fnvOffset64)
	for _, span := range spans {
		hash.writeInt(span.Column)
		hash.writeByte(0)
		hash.writeInt(span.Width)
		hash.writeByte(0)
		hash.writeInt(span.Style)
		hash.writeByte(0)
		hash.writeString(span.Text)
		hash.writeByte(1)
	}
	return uint64(hash)
}
