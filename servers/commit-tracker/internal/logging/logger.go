package logging

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type Logger struct {
	mu     sync.Mutex
	writer io.Writer
}

func New() *Logger {
	return NewWithWriter(os.Stderr)
}

func NewWithWriter(writer io.Writer) *Logger {
	if writer == nil {
		writer = io.Discard
	}
	return &Logger{writer: writer}
}

func (l *Logger) Event(fields map[string]any) {
	if l == nil {
		return
	}

	payload := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		payload[key] = value
	}
	payload["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)

	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.writer.Write(append(encoded, '\n'))
}
