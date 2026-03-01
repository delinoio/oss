//go:build !windows

package transport

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestIsBenignPTYOutputErr(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "closed file error",
			err:  os.ErrClosed,
			want: true,
		},
		{
			name: "raw syscall eio is not automatically benign",
			err:  syscall.EIO,
			want: false,
		},
		{
			name: "ptmx read-close eio",
			err: &os.PathError{
				Op:   "read",
				Path: "/dev/ptmx",
				Err:  syscall.EIO,
			},
			want: true,
		},
		{
			name: "ptmx write eio is not benign",
			err: &os.PathError{
				Op:   "write",
				Path: "/dev/ptmx",
				Err:  syscall.EIO,
			},
			want: false,
		},
		{
			name: "non-ptmx read eio is not benign",
			err: &os.PathError{
				Op:   "read",
				Path: "/tmp/output.bin",
				Err:  syscall.EIO,
			},
			want: false,
		},
		{
			name: "other error",
			err:  errors.New("copy failed"),
			want: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isBenignPTYOutputErr(tc.err)
			if got != tc.want {
				t.Fatalf("unexpected result: got=%v want=%v", got, tc.want)
			}
		})
	}
}
