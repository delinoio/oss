//go:build !windows

package credentials

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestVaultRejectsForeignOwnerDirectoryAndFile(t *testing.T) {
	for _, attack := range []string{"symlink-owner", "shared-owner", "symlink-record", "shared-record"} {
		t.Run(attack, func(t *testing.T) {
			v, n, _ := setup(t)
			ctx := context.Background()
			ref := reference()
			if attack == "symlink-owner" {
				if err := os.Symlink(t.TempDir(), v.ownerPath(ref.Owner)); err != nil {
					t.Fatal(err)
				}
			} else if attack == "shared-owner" {
				if err := os.Mkdir(v.ownerPath(ref.Owner), 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := v.Put(ctx, ref, []byte("protected")); err != nil {
					t.Fatal(err)
				}
				if attack == "shared-record" {
					if err := os.Chmod(v.path(ref), 0644); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Remove(v.path(ref)); err != nil {
						t.Fatal(err)
					}
					other := filepath.Join(t.TempDir(), "unrelated")
					if err := os.WriteFile(other, []byte("do not read or replace"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(other, v.path(ref)); err != nil {
						t.Fatal(err)
					}
				}
			}
			size := len(n.values)
			_, err := v.Put(ctx, ref, []byte("protected"))
			wantCode(t, err, domain.PermissionDenied)
			_, err = v.Get(ctx, ref)
			wantCode(t, err, domain.PermissionDenied)
			wantCode(t, v.Delete(ctx, ref), domain.PermissionDenied)
			if len(n.values) != size {
				t.Fatal("unsafe private file caused native mutation")
			}
		})
	}
}
