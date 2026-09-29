//go:build darwin || linux

package workspace

import (
	"context"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTerminalDirectoryRefusesReplacedPrimary(t *testing.T) {
	m := manager(t)
	input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	err = m.WithTerminalDirectory(context.Background(), input, manifest, ReadRequest{ID: domain.NewID()}, func(path string) error {
		if err := os.Rename(path, path+"-original"); err != nil {
			return err
		}
		return os.Mkdir(path, 0700)
	})
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("terminal startup adopted a replaced primary directory", err)
	}
}
