// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIUsageAccountingNegotiation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("temporary server did not start")
	}
	for _, selection := range [][]string{
		{"usage", "summary", "--accounting-profile", "native-units-v1"},
		{"usage", "summary", "--accounting-profile", "native-units-v1", "--granularity", "day", "--timezone", "Asia/Seoul"},
	} {
		code, result := cliRun(t, root, selection, "")
		if code != 0 || result["result"].(map[string]any)["accounting_profile"] != "USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1" {
			t.Fatal("CLI lost negotiated profile", result)
		}
	}
	code, legacy := cliRun(t, root, []string{"usage", "summary"}, "")
	if code != 0 || legacy["result"].(map[string]any)["accounting_profile"] != "USAGE_ACCOUNTING_PROFILE_UNSPECIFIED" {
		t.Fatal("legacy profile changed", legacy)
	}
	if code, _ := cliRun(t, root, []string{"usage", "summary", "--accounting-profile", "unknown"}, ""); code == 0 {
		t.Fatal("unknown profile accepted")
	}
}
