package chat

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

const maxLineBytes = 8 << 20

type Tail struct {
	path    string
	window  int64
	started bool
	offset  int64
	partial []byte
}

func NewTail(path string, window int64) *Tail {
	return &Tail{path: path, window: window}
}

func (t *Tail) Lines() (lines [][]byte, restarted bool, err error) {
	file, err := os.Open(t.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.Size() < t.offset {
		t.offset, t.partial, t.started, restarted = 0, nil, false, true
	}
	midLine := false
	if !t.started {
		t.started = true
		if t.window > 0 && info.Size() > t.window {
			t.offset, midLine = info.Size()-t.window, true
		}
	}
	if info.Size() == t.offset {
		return nil, restarted, nil
	}
	if _, err := file.Seek(t.offset, io.SeekStart); err != nil {
		return nil, restarted, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, restarted, err
	}
	t.offset += int64(len(data))
	if midLine {
		_, data, _ = bytes.Cut(data, []byte{'\n'})
	}
	data = append(t.partial, data...)
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		t.partial = keepPartial(data)
		return nil, restarted, nil
	}
	t.partial = keepPartial(data[end+1:])
	for _, raw := range bytes.Split(data[:end], []byte{'\n'}) {
		if len(bytes.TrimSpace(raw)) > 0 {
			lines = append(lines, raw)
		}
	}
	return lines, restarted, nil
}

func keepPartial(data []byte) []byte {
	if len(data) > maxLineBytes {
		return nil
	}
	return append([]byte(nil), data...)
}
