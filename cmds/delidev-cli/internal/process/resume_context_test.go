// SPDX-License-Identifier: Apache-2.0
package process

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type preparedBarrierLog struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	once     sync.Once
	prepared func()
}

func (l *preparedBarrierLog) Write(raw []byte) (int, error) {
	l.mu.Lock()
	l.buffer.Write(raw)
	l.mu.Unlock()
	if bytes.Contains(raw, []byte(`"msg":"native process prepared"`)) {
		l.once.Do(l.prepared)
	}
	return len(raw), nil
}
func (l *preparedBarrierLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

func TestResumeRefusesPreparedCancellationAndDeadlineBeforeNativeEffect(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			prepared := cancel
			expected := domain.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				// Observe the original timer cancellation, rather than elapsed wall
				// time: one scheduled P may delay the context timer goroutine.
				prepared = func() { <-ctx.Done() }
				expected = domain.Unavailable
			}
			defer cancel()
			log := &preparedBarrierLog{prepared: prepared}
			cfg := config(t, "marker")
			marker := filepath.Join(t.TempDir(), "native-marker")
			cfg.Env = append(cfg.Env, "DELIDEV_TEST_MARKER="+marker)
			cfg.Logger = slog.New(slog.NewJSONHandler(log, nil))
			h, err := Start(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			if ctx.Err() == nil {
				t.Fatal("fixture did not cancel before Resume")
			}
			if err := h.Resume(); domain.SafeError(err).Code != expected {
				t.Fatal("prepared refusal lost typed result", err)
			}
			select {
			case <-h.Done():
			default:
				t.Fatal("refusal did not join original owner")
			}
			if err := h.Close(); err != nil {
				t.Fatal("cleanup unconfirmed", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("canceled barrier launched native effect", err)
			}
			if strings.Contains(log.String(), `"msg":"native process resumed"`) {
				t.Fatal("canceled barrier claimed resumed")
			}
			if err := ReconcileOwner(cfg.Directory, cfg.OwnerID); err != nil {
				t.Fatal("original ownership not completed", err)
			}
		})
	}
}
