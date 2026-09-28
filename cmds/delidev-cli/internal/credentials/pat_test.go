package credentials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func patFixture(t *testing.T) (*PATStore, *memoryNative, *bytes.Buffer) {
	t.Helper()
	native := &memoryNative{values: map[string][]byte{}}
	logs := &bytes.Buffer{}
	s, err := openPAT(filepath.Join(t.TempDir(), "pats"), domain.NewID(), bytes.Repeat([]byte{41}, 32), native, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, native, logs
}
func patReference() PATRef { return PATRef{ProfileID: domain.NewID(), GenerationID: domain.NewID()} }

func TestPATDirectStoragePreservesExactRetryAndNeverPersistsToken(t *testing.T) {
	s, native, logs := patFixture(t)
	ref, ctx := patReference(), context.Background()
	token := []byte("github_pat_Synthetic_private_token_only_in_native_store")
	if err := s.Put(ctx, ref, token); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(native.values[s.name(ref)], token) {
		t.Fatal("PAT was not stored directly in native service")
	}
	if err := s.Put(ctx, ref, token); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Put(ctx, ref, []byte("different")), domain.Conflict)
	root, scope, key := s.scope.root, s.scope.scope, bytes.Clone(s.key)
	defer clear(key)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openPAT(root, scope, key, native, s.scope.logger)
	if err != nil {
		t.Fatal(err)
	}
	out, err := reopened.Get(ctx, ref)
	if err != nil || !bytes.Equal(out, token) {
		t.Fatal("native token did not survive restart", err)
	}
	clear(out)
	refs, err := reopened.UnremovedReferences(ctx, ref.ProfileID)
	if err != nil || len(refs) != 1 || refs[0] != ref {
		t.Fatal("missing original staged metadata", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(token)
	if bytes.Contains(logs.Bytes(), token) {
		t.Fatal("PAT leaked to logs")
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if bytes.Contains(raw, token) || bytes.Contains(raw, []byte(hex.EncodeToString(digest[:]))) || bytes.Contains(raw, key) {
			t.Error("token, unkeyed digest or owner key leaked to private metadata")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPATUncertainNativeWriteReconcilesOnlyOriginalGeneration(t *testing.T) {
	s, native, _ := patFixture(t)
	ref, ctx := patReference(), context.Background()
	token := []byte("original_private_token")
	native.createFailure, native.commitBeforeFailure = errors.New("raw-private-native-failure"), true
	if s.Put(ctx, ref, token) == nil {
		t.Fatal("uncertain write became success")
	}
	if _, err := s.Get(ctx, ref); err == nil {
		t.Fatal("staged token became usable")
	}
	wantCode(t, s.Put(ctx, ref, []byte("other")), domain.Conflict)
	native.createFailure = nil
	if err := s.Put(ctx, ref, token); err != nil {
		t.Fatal(err)
	}
	delete(native.values, s.name(ref))
	wantCode(t, s.Put(ctx, ref, token), domain.RecoveryRequired)
	if len(native.values) != 0 {
		t.Fatal("missing sealed native token was regenerated")
	}
}

func TestPATDeletionDeniesUseBeforeNativeCleanupAndAcrossRestart(t *testing.T) {
	s, native, _ := patFixture(t)
	ref, ctx := patReference(), context.Background()
	token := []byte("original_private_token")
	if err := s.Put(ctx, ref, token); err != nil {
		t.Fatal(err)
	}
	native.removeFailure = locked()
	wantCode(t, s.Delete(ctx, ref), domain.ConfirmationRequired)
	if len(native.values) != 1 {
		t.Fatal("fixture did not retain locked native entry")
	}
	root, scope, key := s.scope.root, s.scope.scope, bytes.Clone(s.key)
	defer clear(key)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openPAT(root, scope, key, native, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Get(ctx, ref); err == nil {
		t.Fatal("deleting token was read")
	}
	wantCode(t, reopened.Put(ctx, ref, token), domain.RecoveryRequired)
	native.removeFailure = nil
	if err := reopened.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	wantCode(t, reopened.Put(ctx, ref, token), domain.RecoveryRequired)
	refs, err := reopened.UnremovedReferences(ctx, ref.ProfileID)
	if err != nil || len(refs) != 0 || len(native.values) != 0 {
		t.Fatal("completed cleanup remained pending", err)
	}
	newRef := PATRef{ProfileID: ref.ProfileID, GenerationID: domain.NewID()}
	if err := reopened.Put(ctx, newRef, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if len(native.values) != 1 {
		t.Fatal("old deletion removed replacement generation")
	}
}

func TestPATBoundsOwnershipAndForeignNativeValue(t *testing.T) {
	s, native, _ := patFixture(t)
	ref, ctx := patReference(), context.Background()
	for _, value := range [][]byte{nil, []byte("has whitespace"), {0xff}, []byte("line\n"), bytes.Repeat([]byte{'a'}, MaxPATBytes+1)} {
		wantCode(t, s.Put(ctx, ref, value), domain.InvalidArgument)
	}
	if len(native.values) != 0 {
		t.Fatal("invalid input reached native store")
	}
	value := bytes.Repeat([]byte{'b'}, MaxPATBytes)
	if err := s.Put(ctx, ref, value); err != nil {
		t.Fatal(err)
	}
	native.values[s.name(ref)] = []byte("foreign token")
	got, err := s.Get(ctx, ref)
	if got != nil {
		t.Fatal("foreign native bytes escaped validation")
	}
	wantCode(t, err, domain.RecoveryRequired)
	for _, other := range []PATRef{{ProfileID: domain.NewID(), GenerationID: ref.GenerationID}, {ProfileID: ref.ProfileID, GenerationID: domain.NewID()}} {
		if got, err := s.Get(ctx, other); err == nil || got != nil {
			t.Fatal("foreign identity selected original native token")
		}
	}
	if wrappingProfile.service() == patProfile.service() || wrappingProfile.accepts(MaxPATBytes) || !patProfile.accepts(MaxPATBytes) || patProfile.accepts(0) || nativeProfile(99).accepts(64) {
		t.Fatal("native service or size profile boundaries changed")
	}
}

// Call only with a test-owned keychain, fresh Windows references, or the
// explicitly disposable Linux Secret Service fixture. Never enumerate logins.
func exerciseNativePAT(t *testing.T, pat, wrapping nativeStore) {
	t.Helper()
	ctx := context.Background()
	s, err := openPAT(filepath.Join(t.TempDir(), "native-pats"), domain.NewID(), bytes.Repeat([]byte{71}, 32), pat, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, size := range []int{1, 64, MaxPATBytes} {
		ref := patReference()
		token := bytes.Repeat([]byte{'P'}, size)
		defer s.Delete(ctx, ref)
		if err := s.Put(ctx, ref, token); err != nil {
			t.Fatal("direct native PAT write", err)
		}
		if err := s.Put(ctx, ref, token); err != nil {
			t.Fatal("direct native PAT retry", err)
		}
		value, err := pat.get(ctx, s.name(ref))
		if err != nil || !bytes.Equal(value, token) {
			t.Fatal("OS store does not contain exact token bytes", err)
		}
		clear(value)
		value, err = wrapping.get(ctx, s.name(ref))
		clear(value)
		wantCode(t, err, domain.NotFound)
		value, err = s.Get(ctx, ref)
		if err != nil || !bytes.Equal(value, token) {
			t.Fatal("direct native PAT read", err)
		}
		clear(value)
		if err := s.Delete(ctx, ref); err != nil {
			t.Fatal(err)
		}
		_, err = pat.get(ctx, s.name(ref))
		wantCode(t, err, domain.NotFound)
		wantCode(t, s.Put(ctx, ref, token), domain.RecoveryRequired)
	}
}
