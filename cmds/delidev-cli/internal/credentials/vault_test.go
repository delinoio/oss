package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Test-only failure injection. Production never falls back to memory/files for
// wrapping keys, and ordinary tests do not read the user's native credential store.
type memoryNative struct {
	values              map[string][]byte
	createFailure       error
	removeFailure       error
	getFailure          error
	commitBeforeFailure bool
	afterCreate         func()
}

func (m *memoryNative) get(ctx context.Context, n string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.getFailure != nil {
		return nil, m.getFailure
	}
	b, ok := m.values[n]
	if !ok {
		return nil, missing()
	}
	return bytes.Clone(b), nil
}
func (m *memoryNative) create(ctx context.Context, n string, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := m.values[n]; ok {
		return duplicate()
	}
	if m.createFailure == nil || m.commitBeforeFailure {
		m.values[n] = bytes.Clone(b)
		if m.afterCreate != nil {
			m.afterCreate()
		}
	}
	return m.createFailure
}
func (m *memoryNative) remove(ctx context.Context, n string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.removeFailure != nil {
		return m.removeFailure
	}
	delete(m.values, n)
	return nil
}
func setup(t *testing.T) (*Vault, *memoryNative, *bytes.Buffer) {
	t.Helper()
	backend := &memoryNative{values: map[string][]byte{}}
	logs := &bytes.Buffer{}
	v, err := open(filepath.Join(t.TempDir(), "vault"), domain.NewID(), backend, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v, backend, logs
}
func reference() Ref { return Ref{Owner: domain.NewID(), ID: domain.NewID(), Purpose: AccountAPI} }
func wantCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	if err == nil || domain.SafeError(err).Code != code {
		t.Fatalf("got %v; want %s", err, code)
	}
}
func TestVaultDurableRoundTripAndImmutableRetry(t *testing.T) {
	v, backend, logs := setup(t)
	ctx := context.Background()
	ref := reference()
	secret := bytes.Repeat([]byte("account-secret-never-in-files-"), 2000)
	binding, err := v.Put(ctx, ref, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(binding) != 64 {
		t.Fatal("missing keyed receipt binding")
	}
	old, err := os.ReadFile(v.path(ref))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, e := v.Put(ctx, ref, secret)
		if e != nil || got != binding {
			t.Fatalf("retry: %q %v", got, e)
		}
	}
	current, _ := os.ReadFile(v.path(ref))
	if !bytes.Equal(old, current) {
		t.Fatal("retry rewrote immutable ciphertext")
	}
	_, err = v.Put(ctx, ref, []byte("replacement"))
	wantCode(t, err, domain.Conflict)
	v.Close()
	reopened, err := open(v.root, v.scope, backend, v.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	out, err := reopened.Get(ctx, ref)
	if err != nil || !bytes.Equal(out, secret) {
		t.Fatalf("round trip: %v", err)
	}
	clear(out)
	if bytes.Contains(logs.Bytes(), secret[:100]) {
		t.Fatal("secret leaked to logs")
	}
	err = filepath.WalkDir(v.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return security.CheckPrivateDir(path)
		}
		if err = security.RegularPrivate(path); err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if bytes.Contains(b, secret[:100]) {
			t.Fatal("secret leaked to private file")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := reopened.References(ctx, ref.Owner)
	if err != nil || len(refs) != 1 || refs[0] != ref {
		t.Fatalf("references: %v %v", refs, err)
	}
}
func TestNativeWriteUncertaintyReconcilesAfterReopen(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[committed], func(t *testing.T) {
			v, n, _ := setup(t)
			ctx := context.Background()
			ref := reference()
			secret := []byte("unconfirmed-secret")
			n.createFailure = unavailable()
			n.commitBeforeFailure = committed
			_, err := v.Put(ctx, ref, secret)
			wantCode(t, err, domain.Unavailable)
			_, err = v.Get(ctx, ref)
			wantCode(t, err, domain.RecoveryRequired)
			v.Close()
			n.createFailure = nil
			next, err := open(v.root, v.scope, n, v.logger)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if committed {
				_, err = next.Put(ctx, ref, []byte("different"))
				wantCode(t, err, domain.Conflict)
			}
			_, err = next.Put(ctx, ref, secret)
			if err != nil {
				t.Fatal(err)
			}
			out, err := next.Get(ctx, ref)
			if err != nil || !bytes.Equal(out, secret) {
				t.Fatalf("resume: %v", err)
			}
		})
	}
}
func TestDeleteTombstoneBlocksUseAndResurrection(t *testing.T) {
	v, n, _ := setup(t)
	ctx := context.Background()
	ref := reference()
	secret := []byte("delete-me")
	if _, err := v.Put(ctx, ref, secret); err != nil {
		t.Fatal(err)
	}
	n.removeFailure = locked()
	wantCode(t, v.Delete(ctx, ref), domain.ConfirmationRequired)
	if len(n.values) != 1 {
		t.Fatal("fixture removed key despite locked store")
	}
	_, err := v.Get(ctx, ref)
	wantCode(t, err, domain.NotFound)
	_, err = v.Put(ctx, ref, secret)
	wantCode(t, err, domain.Conflict)
	rec, err := v.read(ref)
	if err != nil || len(rec.Ciphertext) != 0 || rec.State != deleting {
		t.Fatalf("tombstone: %+v %v", rec, err)
	}
	v.Close()
	n.removeFailure = nil
	next, err := open(v.root, v.scope, n, v.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = next.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err = next.Delete(ctx, ref); err != nil || len(n.values) != 0 {
		t.Fatalf("delete retry: %v", err)
	}
	_, err = next.Put(ctx, ref, secret)
	wantCode(t, err, domain.Conflict)
}
func TestMissingNativeKeyAndDamagedCiphertextFailClosed(t *testing.T) {
	for _, damage := range []string{"missing-key", "wrong-key", "ciphertext", "scope", "owner", "purpose", "version", "duplicate-json"} {
		t.Run(damage, func(t *testing.T) {
			v, n, _ := setup(t)
			ctx := context.Background()
			ref := reference()
			secret := []byte("secret")
			if _, err := v.Put(ctx, ref, secret); err != nil {
				t.Fatal(err)
			}
			rec, _ := v.read(ref)
			switch damage {
			case "missing-key":
				delete(n.values, v.name(ref))
			case "wrong-key":
				n.values[v.name(ref)][0] ^= 1
			case "ciphertext":
				rec.Ciphertext[15] ^= 1
			case "scope":
				rec.Scope = domain.NewID()
			case "owner":
				rec.Ref.Owner = domain.NewID()
			case "purpose":
				rec.Ref.Purpose = AccountLogin
			case "version":
				rec.Version = 2
			}
			raw, _ := json.Marshal(rec)
			if damage == "duplicate-json" {
				raw = append([]byte(`{"version":1,`), raw[1:]...)
			}
			if err := security.WriteAtomic(v.path(ref), raw); err != nil {
				t.Fatal(err)
			}
			_, err := v.Get(ctx, ref)
			wantCode(t, err, domain.RecoveryRequired)
			_, err = v.Put(ctx, ref, secret)
			if damage == "wrong-key" {
				wantCode(t, err, domain.Conflict)
			} else {
				wantCode(t, err, domain.RecoveryRequired)
			}
		})
	}
}
func TestVaultScopeAndOwnerIsolation(t *testing.T) {
	v, n, _ := setup(t)
	ctx := context.Background()
	ref := reference()
	if _, err := v.Put(ctx, ref, []byte("owner-one")); err != nil {
		t.Fatal(err)
	}
	other := ref
	other.Owner = domain.NewID()
	if _, err := v.Put(ctx, other, []byte("owner-two")); err != nil {
		t.Fatal(err)
	}
	for r, want := range map[Ref]string{ref: "owner-one", other: "owner-two"} {
		out, err := v.Get(ctx, r)
		if err != nil || string(out) != want {
			t.Fatalf("isolation: %v", err)
		}
	}
	_, err := open(v.root, v.scope, n, v.logger)
	wantCode(t, err, domain.Conflict)
	v.Close()
	_, err = open(v.root, domain.NewID(), n, v.logger)
	wantCode(t, err, domain.RecoveryRequired)
	if err = os.Remove(filepath.Join(v.root, "scope.json")); err != nil {
		t.Fatal(err)
	}
	_, err = open(v.root, v.scope, n, v.logger)
	wantCode(t, err, domain.RecoveryRequired)
}
func TestBoundsCancellationAndUntrustedErrors(t *testing.T) {
	v, n, logs := setup(t)
	ref := reference()
	ctx := context.Background()
	for _, secret := range [][]byte{nil, make([]byte, MaxSecretBytes+1)} {
		_, err := v.Put(ctx, ref, secret)
		wantCode(t, err, domain.InvalidArgument)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := v.Put(canceled, ref, []byte("secret"))
	wantCode(t, err, domain.Canceled)
	if len(n.values) != 0 {
		t.Fatal("canceled operation wrote native material")
	}
	n.getFailure = errors.New("native-provider-output-super-secret")
	_, err = v.Put(ctx, ref, []byte("secret"))
	wantCode(t, err, domain.Internal)
	if bytes.Contains(logs.Bytes(), []byte("super-secret")) || bytes.Contains([]byte(err.Error()), []byte("super-secret")) {
		t.Fatal("raw error escaped")
	}
}
func TestConcurrentWritersCannotReplaceCredential(t *testing.T) {
	v, n, _ := setup(t)
	ctx := context.Background()
	ref := reference()
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, err := v.Put(ctx, ref, []byte{byte(i + 1)}); results <- err }(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			wantCode(t, err, domain.Conflict)
		}
	}
	if accepted != 1 || len(n.values) != 1 {
		t.Fatalf("accepted %d writes", accepted)
	}
}
func TestDeleteUnknownReferenceStillPreventsDelayedWrite(t *testing.T) {
	v, _, _ := setup(t)
	ref := reference()
	ctx := context.Background()
	if err := v.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	_, err := v.Put(ctx, ref, []byte("late write"))
	wantCode(t, err, domain.Conflict)
}

func TestCancelAfterNativeCommitRetainsExactRetry(t *testing.T) {
	v, n, _ := setup(t)
	ref := reference()
	ctx, cancel := context.WithCancel(context.Background())
	n.afterCreate = cancel
	_, err := v.Put(ctx, ref, []byte("accepted-native-write"))
	wantCode(t, err, domain.Canceled)
	if len(n.values) != 1 {
		t.Fatal("native result was speculatively deleted")
	}
	n.afterCreate = nil
	_, err = v.Put(context.Background(), ref, []byte("changed-value"))
	wantCode(t, err, domain.Conflict)
	if _, err = v.Put(context.Background(), ref, []byte("accepted-native-write")); err != nil {
		t.Fatal(err)
	}
}

func TestVaultRecoversOnlyUnpublishedInitialPinScratch(t *testing.T) {
	for _, contents := range []string{"", `{"scope":`, `{"scope":"` + string(domain.NewID()) + `"}`} {
		t.Run(contents, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "vault")
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			scratch := filepath.Join(root, ".pending-123456789")
			if err := os.WriteFile(scratch, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			v, err := open(root, domain.NewID(), &memoryNative{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			v.Close()
			if _, err := os.Lstat(scratch); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("initial scratch retained", err)
			}
		})
	}
	for _, entry := range []string{"owner", "unknown", "linked", "oversized"} {
		t.Run(entry, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "vault")
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			scratch := filepath.Join(root, ".pending-123")
			if err := os.WriteFile(scratch, nil, 0600); err != nil {
				t.Fatal(err)
			}
			switch entry {
			case "owner":
				if err := os.Mkdir(filepath.Join(root, string(domain.NewID())), 0700); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				if err := os.WriteFile(filepath.Join(root, ".pending-foreign"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "linked":
				if err := os.Symlink(scratch, filepath.Join(root, ".pending-456")); err != nil {
					t.Skip(err)
				}
			case "oversized":
				if err := os.WriteFile(filepath.Join(root, ".pending-456"), make([]byte, 1025), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := open(root, domain.NewID(), &memoryNative{}, nil)
			wantCode(t, err, domain.RecoveryRequired)
			if _, err := os.Lstat(scratch); err != nil {
				t.Fatal("recovery evidence removed", err)
			}
		})
	}
}
