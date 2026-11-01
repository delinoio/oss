// SPDX-License-Identifier: Apache-2.0

//go:build darwin || linux

package clone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCapabilityClassification(t *testing.T) {
	for _, err := range []error{unix.ENOSPC, unix.EACCES, unix.EPERM, unix.EIO, unix.EINVAL, unix.EBADF} {
		got := capabilityError(err)
		if errors.Is(got, ErrUnsupported) || !errors.Is(got, err) {
			t.Fatalf("misclassified %v: %v", err, got)
		}
	}
	for _, err := range []error{unix.EXDEV, unix.ENOTSUP, unix.ENOSYS} {
		if !errors.Is(capabilityError(err), ErrUnsupported) {
			t.Fatalf("missing capability error: %v", err)
		}
	}
}

func TestRejectFIFOWithoutBlocking(t *testing.T) {
	src, dst := fixture(t)
	if err := unix.Mkfifo(filepath.Join(src, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tree(context.Background(), src, dst, true, unsupported); err == nil {
		t.Fatal("accepted FIFO")
	}
	absent(t, dst)
}

func TestReadOnlyDirectoryCleanup(t *testing.T) {
	src, dst := fixture(t)
	readonly := filepath.Join(src, "0-readonly")
	if err := os.Mkdir(readonly, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readonly, "item"), nil, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readonly, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readonly, 0700) })
	failure := unix.ENOSPC
	_, err := tree(context.Background(), src, dst, true, func(ctx context.Context, f *os.File, path string) error {
		if filepath.Base(path) == "a" {
			return failure
		}
		return copyFile(ctx, f, path)
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	absent(t, dst)
}
