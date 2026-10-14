// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestTerminalPromptIntegrationOnlyAppliesToGeneralChat(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	manager := newTerminalManager(context.Background(), Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, nil, Credential{}, domain.NewID())
	args, env := []string{"-i"}, []string{"HOME=/home/account"}
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		got := manager.promptIntegration(terminal.Assignment{ID: domain.NewID(), Manifest: &workspace.Manifest{Type: kind}}, "/bin/zsh", cwd, args, env)
		if got.Outcome != "" || !reflect.DeepEqual(got.Args, args) || !reflect.DeepEqual(got.Env, env) {
			t.Fatalf("%s terminal must keep its native launch: %+v", kind, got)
		}
		if err := got.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	chat := manager.promptIntegration(terminal.Assignment{ID: domain.NewID(), Manifest: &workspace.Manifest{Type: domain.GeneralChat}}, "/bin/sh", cwd, args, env)
	if chat.Outcome != terminal.PromptUnsupported || !reflect.DeepEqual(chat.Args, args) || !reflect.DeepEqual(chat.Env, env) {
		t.Fatalf("an unknown shell must keep its native launch: %+v", chat)
	}
}
