package posthogcloud

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

const maxEventBytes = 32 << 20

var errEventTooLarge = errors.New("a stream event is larger than 32 MB")

type Event struct {
	ID   string
	Name string
	Data []byte
}

type EventReader struct {
	reader *bufio.Reader
}

func NewEventReader(r io.Reader) *EventReader {
	return &EventReader{reader: bufio.NewReaderSize(r, 64<<10)}
}

func (r *EventReader) Next() (Event, error) {
	var event Event
	var data bytes.Buffer
	hasData := false
	for {
		line, err := r.readLine()
		if err != nil {
			return Event{}, err
		}
		if len(line) == 0 {
			if hasData || event.Name != "" || event.ID != "" {
				event.Data = data.Bytes()
				return event, nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			event.Name = string(value)
		case "id":
			event.ID = string(value)
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.Write(value)
			hasData = true
			if data.Len() > maxEventBytes {
				return Event{}, errEventTooLarge
			}
		}
	}
}

func (r *EventReader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxEventBytes {
			return nil, errEventTooLarge
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return nil, err
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		return bytes.TrimSuffix(line, []byte("\r")), nil
	}
}
