// SPDX-License-Identifier: Apache-2.0
package process

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOwnedScopeUsesExplicitSessionContext(t *testing.T) {
	for _, session := range []SessionContext{BackgroundSession, CurrentUserGUI} {
		t.Run(string(session), func(t *testing.T) {
			expected := "Background"
			if session == CurrentUserGUI && launchctl("print", fmt.Sprintf("gui/%d", os.Getuid())) == nil {
				expected = "Aqua"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var output bytes.Buffer
			cfg := Config{Directory: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), Executable: "/bin/launchctl", Args: []string{"managername"}, Env: []string{"PATH=/usr/bin:/bin"}, Cwd: t.TempDir(), Stdout: &output, SessionContext: session}
			if err := Run(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(output.String()); got != expected {
				t.Fatalf("owned scope manager = %q, want %q", got, expected)
			}
			if err := ReconcileOwner(cfg.Directory, cfg.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
