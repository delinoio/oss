package workspace

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/windows"
)

func TestTerminalDirectoryBlocksPrimaryReplacementWhileAnchored(t *testing.T) {
	m := manager(t)
	input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	attempted := false
	err = m.WithTerminalDirectory(context.Background(), input, manifest, ReadRequest{ID: domain.NewID()}, func(path string) error {
		attempted = true
		// The pinned Go Windows OpenRoot handle excludes FILE_SHARE_DELETE.
		// Replacement is prevented during launch instead of detected afterward
		// as on Unix. Require that specific native protection, not any failure.
		if err := os.Rename(path, path+"-original"); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("anchored directory replacement was not blocked by its open handle: %v", err)
		}
		return nil
	})
	if err != nil || !attempted {
		t.Fatal("the original anchored primary directory was unavailable", err)
	}
	// Prove the scope released its anchor and that the earlier denial was not
	// an unrelated permission or filesystem failure.
	if err := os.Rename(manifest.PrimaryPath, manifest.PrimaryPath+"-original"); err != nil {
		t.Fatal("the primary directory anchor was not released", err)
	}
}
