// SPDX-License-Identifier: Apache-2.0
package process

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOwnedScopePreservesOriginalGUIManager(t *testing.T) {
	original, err := exec.Command("/bin/launchctl", "managername").Output()
	if err != nil {
		t.Fatal(err)
	}
	expected := "Background"
	if strings.TrimSpace(string(original)) == "Aqua" {
		expected = "Aqua"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	cfg := Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: "/bin/launchctl", Args: []string{"managername"}, Env: []string{"PATH=/usr/bin:/bin"}, Cwd: t.TempDir(), Stdout: &output}
	if err := Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(output.String()); got != expected {
		t.Fatalf("owned scope manager = %q, want %q", got, expected)
	}
	if err := ReconcileOwner(cfg.Directory, cfg.OwnerID); err != nil {
		t.Fatal(err)
	}
}
