// SPDX-License-Identifier: Apache-2.0
package nativewire

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

type cancelPreparedWireLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	cancel context.CancelFunc
	once   sync.Once
}

func (l *cancelPreparedWireLog) Write(raw []byte) (int, error) {
	l.mu.Lock()
	l.buffer.Write(raw)
	l.mu.Unlock()
	if bytes.Contains(raw, []byte(`"msg":"native process prepared"`)) {
		l.once.Do(l.cancel)
	}
	return len(raw), nil
}
func TestWirePreparedCancellationReturnsNoConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &cancelPreparedWireLog{cancel: cancel}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := process.Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: executable, Args: []string{"__delidev_wire_fixture", "normal"}, Cwd: t.TempDir(), Env: []string{}, Logger: slog.New(slog.NewJSONHandler(log, nil))}
	connection, err := Start(ctx, config)
	if connection != nil || domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("canceled barrier exposed a native connection", err)
	}
	if err := process.ReconcileOwner(config.Directory, config.OwnerID); err != nil {
		t.Fatal("owner cleanup not joined", err)
	}
	log.mu.Lock()
	text := log.buffer.String()
	log.mu.Unlock()
	if strings.Contains(text, `"msg":"native process resumed"`) {
		t.Fatal("canceled wire resumed native command")
	}
}
