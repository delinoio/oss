// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"testing"
)

func TestCurrentInlineLayout(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	s, e := Open(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var v int
	if e = s.db.QueryRow("PRAGMA user_version").Scan(&v); e != nil || v != 32 {
		t.Fatalf("version %d: %v", v, e)
	}
	var n int
	if e = s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('model_suppressions','model_display','model_native')").Scan(&n); e != nil || n != 0 {
		t.Fatalf("model registry remains: %d %v", n, e)
	}
	identity := domain.ModelIdentity{ProviderID: domain.NewID(), NativeID: "exact/native"}
	r, e := inlineModelRecord(identity.Key())
	if e != nil {
		t.Fatal(e)
	}
	m, e := Decode[domain.Model](r)
	if e != nil || m.NativeID != identity.NativeID || m.ProviderID != identity.ProviderID {
		t.Fatal("source key changed")
	}
	if _, e = inlineModelRecord(domain.NewID()); e == nil {
		t.Fatal("legacy resource UUID adopted")
	}
}
