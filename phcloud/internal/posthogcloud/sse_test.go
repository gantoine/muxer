package posthogcloud

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEventReaderParsesFrames(t *testing.T) {
	reader := NewEventReader(strings.NewReader(": hello\r\n\r\nevent: end\r\nid: r-1\r\ndata: {\"a\":1,\r\ndata: \"b\":2}\r\n\r\ndata:" + strings.Repeat("x", 200_000) + "\n\nid: r-2\n"))
	end, err := reader.Next()
	if err != nil || end.Name != "end" || end.ID != "r-1" || string(end.Data) != "{\"a\":1,\n\"b\":2}" {
		t.Fatalf("end = %+v %v", end, err)
	}
	long, err := reader.Next()
	if err != nil || len(long.Data) != 200_000 || long.ID != "" {
		t.Fatalf("a long line must arrive whole: %d %v", len(long.Data), err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("an unfinished frame must end with EOF: %v", err)
	}
}
