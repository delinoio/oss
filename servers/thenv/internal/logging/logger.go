package logging

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type Logger struct {
	mu sync.Mutex
}

func New() *Logger {
	return &Logger{}
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
	_, _ = os.Stderr.Write(append(encoded, '\n'))
}
