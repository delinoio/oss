package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
)

type Logger struct {
	mu sync.Mutex
	f  *os.File
}

func New(stateRoot string) (*Logger, error) {
	logDir := filepath.Join(stateRoot, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, errmsg.Error(errmsg.Runtime("create log dir", err, map[string]any{
			"state_root": stateRoot,
			"log_dir":    logDir,
		}), nil)
	}
	if err := os.Chmod(logDir, 0o700); err != nil {
		return nil, errmsg.Error(errmsg.Runtime("chmod log dir", err, map[string]any{
			"state_root": stateRoot,
			"log_dir":    logDir,
		}), nil)
	}

	path := filepath.Join(logDir, "derun.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, errmsg.Error(errmsg.Runtime("open log file", err, map[string]any{
			"state_root": stateRoot,
			"log_path":   path,
		}), nil)
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
