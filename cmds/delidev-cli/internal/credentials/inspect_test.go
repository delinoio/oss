package credentials

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestInspectionCannotCreateRepairOrModifyVault(t *testing.T) {
	v, native, _ := setup(t)
	ctx := context.Background()
	ref := reference()
	if _, err := v.Put(ctx, ref, []byte("owned-inspection-secret")); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(v.path(ref), maxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openExisting(v.root, v.scope, native, v.logger); err == nil {
		t.Fatal("live exclusive scope bypassed")
	}
	v.Close()
	inspection, err := openExisting(v.root, v.scope, native, v.logger)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := inspection.Get(ctx, ref)
	if err != nil || string(secret) != "owned-inspection-secret" {
		t.Fatal("sealed credential unreadable", err)
	}
	clear(secret)
	if _, err := inspection.Put(ctx, ref, []byte("replacement")); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal(err)
	}
	if err := inspection.Delete(ctx, ref); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal(err)
	}
	inspection.Close()
	current, err := os.ReadFile(v.path(ref))
	if err != nil || !bytes.Equal(raw, current) {
		t.Fatal("inspection changed sealed state", err)
	}
	if _, err := openExisting(filepath.Join(t.TempDir(), "absent"), v.scope, native, v.logger); err == nil {
		t.Fatal("missing scope created")
	}
	if _, err := openExisting(v.root, domain.NewID(), native, v.logger); err == nil {
		t.Fatal("foreign scope rebound")
	}
	if err := os.Remove(filepath.Join(v.root, "scope.json")); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(v.root, ".pending-12345")
	if err := security.WriteAtomic(scratch, []byte(`{"scope":"unfinished"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := openExisting(v.root, v.scope, native, v.logger); err == nil {
		t.Fatal("missing pin repaired")
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("scratch was cleaned", err)
	}
	if _, err := os.Stat(filepath.Join(v.root, "scope.json")); !os.IsNotExist(err) {
		t.Fatal("pin recreated", err)
	}
}
