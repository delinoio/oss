// SPDX-License-Identifier: Apache-2.0

package clone

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, dst := filepath.Join(parent, "source 한글"), filepath.Join(parent, "destination space")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "nested/b", "empty"} {
		data := []byte("original cache data")
		if name == "empty" {
			data = nil
		}
		if err := os.WriteFile(filepath.Join(src, name), data, 0640); err != nil {
			t.Fatal(err)
		}
	}
	return src, dst
}

func unsupported(context.Context, *os.File, string) error { return ErrUnsupported }

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected absent %s: %v", path, err)
	}
}

func TestCopyPreservesFreshnessAndIsolation(t *testing.T) {
	src, dst := fixture(t)
	stamp := time.Unix(1700000000, 123456700)
	for _, path := range []string{"a", "nested/b", "nested", "."} {
		if err := os.Chtimes(filepath.Join(src, path), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	result, err := tree(context.Background(), src, dst, true, unsupported)
	if err != nil {
		t.Fatal(err)
	}
	if result != (Result{CopiedFiles: 3, Mode: ModeCopy}) {
		t.Fatal(result)
	}
	for _, name := range []string{"a", "nested/b", "empty", "nested", "."} {
		original, err := os.Stat(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		derived, err := os.Stat(filepath.Join(dst, name))
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(original, derived) {
			t.Fatal("shared inode", name)
		}
		if !original.ModTime().Equal(derived.ModTime()) || original.Mode().Perm() != derived.Mode().Perm() {
			t.Fatalf("metadata changed for %s: %v / %v", name, original, derived)
		}
	}
	assertIsolation(t, src, dst)
}

func assertIsolation(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dst, "a"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(src, "a"))
	if err != nil || string(data) != "original cache data" {
		t.Fatalf("source mutated: %q %v", data, err)
	}
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(src, "nested/b"))
	if err != nil || string(data) != "original cache data" {
		t.Fatalf("source removed: %q %v", data, err)
	}
}

func TestFallbackRequiresExplicitAuthorization(t *testing.T) {
	src, dst := fixture(t)
	_, err := tree(context.Background(), src, dst, false, unsupported)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	absent(t, dst)
}

func TestOperationalFailuresNeverCopy(t *testing.T) {
	for _, failure := range []error{fs.ErrPermission, errors.New("injected disk full"), errors.New("injected I/O failure")} {
		t.Run(failure.Error(), func(t *testing.T) {
			src, dst := fixture(t)
			calls := 0
			_, err := tree(context.Background(), src, dst, true, func(ctx context.Context, f *os.File, path string) error {
				calls++
				if calls == 1 {
					return copyFile(ctx, f, path)
				}
				if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
				return failure
			})
			if !errors.Is(err, failure) || calls != 2 {
				t.Fatalf("%v calls %d", err, calls)
			}
			absent(t, dst)
			if data, err := os.ReadFile(filepath.Join(src, "a")); err != nil || string(data) != "original cache data" {
				t.Fatal("source changed", err)
			}
		})
	}
}

func TestPartialNativeFallbackStartsFresh(t *testing.T) {
	src, dst := fixture(t)
	calls := 0
	result, err := tree(context.Background(), src, dst, true, func(ctx context.Context, f *os.File, path string) error {
		calls++
		if calls == 1 {
			return copyFile(ctx, f, path)
		}
		if err := os.WriteFile(path, bytes.Repeat([]byte("junk"), 1000), 0600); err != nil {
			return err
		}
		_, _ = f.Seek(3, 0)
		return ErrUnsupported
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != (Result{ClonedFiles: 1, CopiedFiles: 2, Mode: ModeMixed}) {
		t.Fatal(result)
	}
	for _, name := range []string{"a", "empty", "nested/b"} {
		want, _ := os.ReadFile(filepath.Join(src, name))
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
}

func TestCancellationCleansStage(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "during"}[before], func(t *testing.T) {
			src, dst := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if before {
				cancel()
			}
			_, err := tree(ctx, src, dst, true, func(ctx context.Context, f *os.File, path string) error {
				err := copyFile(ctx, f, path)
				cancel()
				return err
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			absent(t, dst)
		})
	}
}

func TestCopyChecksCancellationBetweenReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := contextReader{ctx, bytes.NewBufferString("contents")}
	cancel()
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExistingDestinationAndOverlap(t *testing.T) {
	src, dst := fixture(t)
	if err := os.Mkdir(dst, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dst, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{dst, src, filepath.Join(src, "inside")} {
		if _, err := tree(context.Background(), src, target, true, unsupported); err == nil {
			t.Fatalf("accepted %s", target)
		}
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("existing cache damaged", err)
	}
	absent(t, filepath.Join(src, "inside"))
}

func TestRejectSymlinks(t *testing.T) {
	src, dst := fixture(t)
	if err := os.Symlink("a", filepath.Join(src, "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	if _, err := tree(context.Background(), src, dst, true, unsupported); err == nil {
		t.Fatal("accepted symlink")
	}
	absent(t, dst)
}

func TestSourceMutationFails(t *testing.T) {
	src, dst := fixture(t)
	_, err := tree(context.Background(), src, dst, false, func(ctx context.Context, f *os.File, path string) error {
		if err := copyFile(ctx, f, path); err != nil {
			return err
		}
		relative, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(src, relative), []byte("changed by unmanaged writer"), 0600)
	})
	if err == nil {
		t.Fatal("accepted changed source")
	}
	absent(t, dst)
}

func TestNativeTree(t *testing.T) {
	src, dst := fixture(t)
	// A non-aligned, multi-cluster file exercises ReFS EOF handling on native runs.
	payload := bytes.Repeat([]byte("native clone fixture"), 10001)
	if err := os.WriteFile(filepath.Join(src, "large"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Tree(context.Background(), src, dst, false)
	if errors.Is(err, ErrUnsupported) && os.Getenv("CCOW_TEST_NATIVE") != "1" {
		t.Skipf("native supported filesystem unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if result != (Result{ClonedFiles: 4, Mode: ModeClone}) {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(dst, "large"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("native content mismatch", err)
	}
	a, _ := os.Stat(filepath.Join(src, "large"))
	b, _ := os.Stat(filepath.Join(dst, "large"))
	if os.SameFile(a, b) {
		t.Fatal("native clone created a hard link")
	}
	if !a.ModTime().Equal(b.ModTime()) || a.Mode().Perm() != b.Mode().Perm() {
		t.Fatal("native clone changed freshness metadata")
	}
	assertIsolation(t, src, dst)
	t.Logf("native clone and write/delete isolation passed on %s/%s; physical allocation not measured", runtime.GOOS, runtime.GOARCH)
}

func TestReFSChunkPlanning(t *testing.T) {
	for _, cluster := range []int64{4096, 65536} {
		for _, size := range []int64{0, 1, 4095, 4096, 65537, 1 << 32, (1 << 33) + 1} {
			length, err := alignedLength(size, cluster)
			if err != nil {
				t.Fatal(err)
			}
			if length < size || length-size >= cluster {
				t.Fatalf("bad rounding %d", length)
			}
			end := int64(0)
			err = cloneChunks(context.Background(), length, cluster, func(offset, count int64) error {
				if offset != end || offset%cluster != 0 || count%cluster != 0 || count <= 0 || count >= 1<<32 {
					t.Fatalf("bad chunk %d %d", offset, count)
				}
				end += count
				return nil
			})
			if err != nil || end != length {
				t.Fatal(end, length, err)
			}
		}
	}
	for _, args := range [][2]int64{{-1, 4096}, {1, 0}, {1, 1 << 32}, {math.MaxInt64, 4096}} {
		if _, err := alignedLength(args[0], args[1]); err == nil {
			t.Fatal("accepted invalid length", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := cloneChunks(ctx, 1<<33, 4096, func(int64, int64) error { calls++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(err, calls)
	}
	failure := errors.New("chunk failure")
	if err := cloneChunks(context.Background(), 1<<33, 4096, func(int64, int64) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestAncestorAliasCannotRecurse(t *testing.T) {
	src, _ := fixture(t)
	alias := filepath.Join(filepath.Dir(src), "alias")
	if err := os.Symlink(src, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	dst := filepath.Join(alias, "inside")
	if _, err := tree(context.Background(), src, dst, true, unsupported); err == nil {
		t.Fatal("accepted aliased recursion")
	}
	absent(t, dst)
}

func TestEmptyTree(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, dst := filepath.Join(parent, "src"), filepath.Join(parent, "dst")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := tree(context.Background(), src, dst, false, func(context.Context, *os.File, string) error { t.Fatal("cloned directory as file"); return nil })
	if err != nil || result != (Result{Mode: ModeClone}) {
		t.Fatal(result, err)
	}
}

func TestSourceHardlinksBecomeIndependentFiles(t *testing.T) {
	src, dst := fixture(t)
	if err := os.Link(filepath.Join(src, "a"), filepath.Join(src, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := tree(context.Background(), src, dst, true, unsupported); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(dst, "a"))
	b, _ := os.Stat(filepath.Join(dst, "alias"))
	if os.SameFile(a, b) {
		t.Fatal("retained source hardlink relationship")
	}
	assertIsolation(t, src, dst)
}
