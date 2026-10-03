// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// Failure snapshots may race a late owned-process exit log after initialization
// fails. Keep both access paths synchronized while retaining every original line.
type fixtureLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *fixtureLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *fixtureLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestFixtureLogSnapshotDuringNativeExit(t *testing.T) {
	config, logs := fixtureConfig(t, "concurrent-log")
	const count = 256
	start := make(chan struct{})
	var joined sync.WaitGroup
	joined.Add(2)
	go func() {
		defer joined.Done()
		<-start
		for i := 0; i < count; i++ {
			config.Process.Logger.Info("native process exited", "stage", "fixture", "sequence", i)
		}
	}()
	go func() {
		defer joined.Done()
		<-start
		for i := 0; i < count; i++ {
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				if line != "" && !json.Valid([]byte(line)) {
					t.Error("concurrent failure snapshot contained an incomplete structured log")
					return
				}
			}
		}
	}()
	close(start)
	joined.Wait()
	if strings.Count(logs.String(), "\n") != count {
		t.Fatal("concurrent failure snapshots lost original exit logs")
	}
}
