// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
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

func TestEarlierDatabaseIsRejectedWithoutChangingOriginalBytes(t *testing.T) {
	for _, version := range []int{1, 26, 27, 28, 29, 30, 31} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "state.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(fmt.Sprintf("PRAGMA application_id=%d;PRAGMA user_version=%d;CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT);INSERT INTO metadata VALUES('original','preserve')", applicationID, version)); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				opened, err := Open(context.Background(), root)
				if err == nil {
					opened.Close()
					t.Fatal("earlier database adopted")
				}
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal(err)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("original changed", err)
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
					t.Fatal("original sidecar created", suffix, err)
				}
			}
		})
	}
}

func TestCurrentInlineRejectsRetiredModelResourceAuthority(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	identity := domain.ModelIdentity{ProviderID: domain.NewID(), NativeID: "exact/native"}
	assertUnsupported := func(err error) {
		t.Helper()
		if domain.SafeError(err).Code != domain.Unsupported {
			t.Fatalf("retired resource accepted: %v", err)
		}
	}
	err := s.Read(ctx, func(tx *Tx) error {
		_, err := tx.Get(domain.ModelKind, identity.Key())
		if err != nil {
			return err
		}
		assertUnsupported(tx.ValidateModelIdentity(identity.Key(), domain.Model{ProviderID: identity.ProviderID, NativeID: identity.NativeID}))
		_, err = tx.ModelSuppressed(identity.ProviderID, identity.NativeID)
		assertUnsupported(err)
		_, err = tx.ModelsForProvider(identity.ProviderID)
		assertUnsupported(err)
		assertUnsupported(tx.Delete(domain.ModelKind, identity.Key(), 1))
		_, err = tx.Put(domain.ModelKind, identity.Key(), 0, "", "", domain.Model{ProviderID: identity.ProviderID, NativeID: identity.NativeID})
		assertUnsupported(err)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.SearchModels(ctx, ModelSearch{Limit: 10})
	assertUnsupported(err)
	_, err = s.ResolveModel(ctx, identity.NativeID, identity.ProviderID)
	assertUnsupported(err)
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM entities WHERE kind='model'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("registry mutation: %d %v", count, err)
	}
}
