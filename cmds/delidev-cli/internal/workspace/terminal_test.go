package workspace

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type terminalWorkspaceLogs struct {
	sync.Mutex
	bytes.Buffer
}

func (b *terminalWorkspaceLogs) Write(raw []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(raw)
}

func (b *terminalWorkspaceLogs) snapshot() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

func TestTerminalDirectoryUsesOriginalPrimaryAndDoesNotTakeAgentLease(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.GeneralChat, domain.Local, domain.Worktree} {
		t.Run(string(kind), func(t *testing.T) {
			m := manager(t)
			logs := &terminalWorkspaceLogs{}
			m.Logger = slog.New(slog.NewJSONHandler(logs, nil))
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(logs.snapshot())
				}
			})
			input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: kind}
			if kind != domain.GeneralChat {
				input, _ = requestFor(repository(t))
				input.Type = kind
				second := domain.NewID()
				input.Repositories = append(input.Repositories, RepositorySpec{ID: second, Checkout: repository(t), Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
				input.PrimaryRepository = second
			}
			for i := range input.Repositories {
				resolved, err := filepath.EvalSymlinks(input.Repositories[i].Checkout)
				if err != nil {
					t.Fatal(err)
				}
				input.Repositories[i].Checkout = resolved
			}
			manifest, err := m.Prepare(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			err = m.WithTerminalDirectory(context.Background(), input, manifest, ReadRequest{ID: domain.NewID()}, func(path string) error {
				if path != manifest.PrimaryPath || path != lease.WorkingDirectory() {
					t.Fatal("terminal targeted a secondary or foreign root")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
