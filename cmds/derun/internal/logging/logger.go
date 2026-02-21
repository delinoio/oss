package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu sync.Mutex
	f  *os.File
}

func New(stateRoot string) (*Logger, error) {
	logDir := filepath.Join(stateRoot, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	if err := os.Chmod(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod log dir: %w", err)
	}

	path := filepath.Join(logDir, "derun.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return &Logger{f: f}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

func (l *Logger) Event(name string, fields map[string]any) {
	if l == nil || l.f == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"] = name
	fields["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)

	b, err := json.Marshal(fields)
	if err != nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.Write(append(b, '\n'))
}
