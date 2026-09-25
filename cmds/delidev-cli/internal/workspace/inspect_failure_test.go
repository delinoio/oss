package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInspectPropagatesRemoteHeadExecutionFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, tc := range []struct {
		name, command string
		want          domain.Code
	}{
		{"absent", "exit 1", ""},
		{"invalid", "exit 128", domain.Unavailable},
		{"timeout", "exec sleep 5", domain.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "git-fixture")
			script := "#!/bin/sh\ncase \"$7\" in\nrev-parse) printf '%s\\n' \"$2\";;\nremote) printf 'origin\\n';;\nsymbolic-ref) " + tc.command + ";;\n*) exit 128;;\nesac\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			g := Git{Executable: executable, ProcessRoot: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Timeout: time.Second}
			got, err := g.Inspect(context.Background(), root)
			if tc.want == "" {
				if err != nil || len(got.Remotes) != 1 || len(got.DefaultRefs) != 0 {
					t.Fatalf("absent default: %+v %v", got, err)
				}
			} else if err == nil || domain.SafeError(err).Code != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}
