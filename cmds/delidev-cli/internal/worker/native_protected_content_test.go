// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func init() {
	if len(os.Args) != 2 || os.Args[1] != "--worker-protected-content-fixture" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": os.Getenv("ANTHROPIC_AUTH_TOKEN")}}}})
	for {
		time.Sleep(time.Second)
	}
}

func TestWorkerProtectedNativeFrameCannotEnterDurableContent(t *testing.T) {
	publisher, rpc := newClaudeContentFixture(t)
	priorEvents := len(rpc.events)
	priorOutbox, err := security.ReadPrivate(publisher.binding.publisher.path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "runtime")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	env, err := harness.PrivateRuntimeEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	secret := "private-original-worker-runtime-token"
	env = append(env, "ANTHROPIC_AUTH_TOKEN="+secret)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: binary, Cwd: root, Env: env, Args: []string{"--worker-protected-content-fixture"}, ProtectedValues: []string{secret}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := claude.StartStream(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Next(ctx)
	if err == nil || domain.SafeError(err).Code != domain.Unsupported || len(event.Body) != 0 {
		t.Fatal("protected native frame reached Worker content receiver", err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err = process.ReconcileOwner(cfg.Directory, cfg.OwnerID); err != nil {
		t.Fatal(err)
	}
	afterOutbox, err := security.ReadPrivate(publisher.binding.publisher.path, 1<<20)
	if err != nil || len(rpc.events) != priorEvents || !bytes.Equal(priorOutbox, afterOutbox) {
		t.Fatal("refusal changed durable publication", err)
	}
}
