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
			name: "syscall eio",
			err:  syscall.EIO,
			want: true,
		},
		{
			name: "string input output error",
			err:  errors.New("read /dev/ptmx: input/output error"),
			want: true,
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
