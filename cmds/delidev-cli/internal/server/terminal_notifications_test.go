// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestTerminalNotificationsWakeBeforeSafetyTimer(t *testing.T) {
	for _, stage := range []terminalWakeStage{terminalStoreChanged, terminalOutputAppended} {
		t.Run(string(stage), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			changed := make(chan struct{})
			output := make(chan struct{})
			// Publication occurs after the snapshot and before entering the wait.
			// A nil safety channel deliberately never supplies a polling tick.
			if stage == terminalStoreChanged {
				close(changed)
			} else {
				close(output)
			}
			got, live := waitTerminalChange(ctx, nil, changed, output, nil)
			if !live || got != stage {
				t.Fatal("missed notification captured before publication", got, live)
			}
		})
	}
}

func TestTerminalOutputBroadcastWakesAllSnapshotsWithoutWaiterRetention(t *testing.T) {
	service := &Service{}
	service.terminalOutputMu.Lock()
	first, second := service.terminalOutputNotification(), service.terminalOutputNotification()
	if first != second {
		t.Fatal("observers acquired independent output pollers")
	}
	service.notifyTerminalOutput()
	next := service.terminalOutputNotification()
	service.terminalOutputMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, snapshot := range []<-chan struct{}{first, second} {
		stage, live := waitTerminalChange(ctx, nil, nil, snapshot, nil)
		if !live || stage != terminalOutputAppended {
			t.Fatal("broadcast missed an attached snapshot")
		}
	}
	select {
	case <-next:
		t.Fatal("new snapshot inherited false progress")
	default:
	}
	service.terminalOutputMu.Lock()
	for i := 0; i < 1000; i++ {
		service.notifyTerminalOutput()
	}
	if service.terminalOutputs != nil || service.terminalOutputOrder.Len() != 0 {
		t.Fatal("broadcast retained terminal or observer records")
	}
	service.terminalOutputMu.Unlock()
}

func TestTerminalNotificationWaitPreservesCancellationAndPrimaryLifetime(t *testing.T) {
	for _, primaryEnded := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		primary := make(chan struct{})
		if primaryEnded {
			close(primary)
		} else {
			cancel()
		}
		_, live := waitTerminalChange(ctx, primary, nil, nil, nil)
		cancel()
		if live {
			t.Fatal("notification wait outlived original stream")
		}
	}
}

func TestTerminalWakeDiagnosticsAreContentFree(t *testing.T) {
	var output bytes.Buffer
	service := &Service{logger: slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	service.logTerminalWake(terminalOutputStream, terminalOutputAppended, 1500*time.Microsecond)
	for _, field := range []string{`"stage":"output_appended"`, `"duration_ms":1`, `"stream":"output"`} {
		if !strings.Contains(output.String(), field) {
			t.Fatal("missing bounded stream diagnostic", field)
		}
	}
	for _, forbidden := range []string{"terminal_id", "machine_id", "data", "input", "output_bytes"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatal("diagnostic exposes terminal content or identity", forbidden)
		}
	}
}
