// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestStreamPreparedCancellationReturnsNoConnection(t *testing.T) {
	cfg, _ := fixtureConfig(t, "stream")
	cfg.Process.Args = []string{"--delidev-claude-stream-fixture", "normal"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &cancelPreparedProbeLog{cancel: cancel}
	cfg.Process.Logger = slog.New(slog.NewJSONHandler(log, nil))
	stream, err := StartStream(ctx, cfg.Process)
	if stream != nil || domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("canceled barrier exposed Claude stream", err)
	}
	if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
		t.Fatal("owner cleanup not joined", err)
	}
	if strings.Contains(log.String(), `"msg":"native process resumed"`) {
		t.Fatal("canceled stream resumed native command")
	}
}
