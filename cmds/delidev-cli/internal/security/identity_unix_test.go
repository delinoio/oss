// SPDX-License-Identifier: Apache-2.0
//go:build unix

package security

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureNativeIdentityMetadataOnly(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "unreadable")
	if err := os.WriteFile(private, []byte("private"), 0000); err != nil {
		t.Fatal(err)
	}
	id, err := CaptureNativeIdentity(private)
	if err != nil || id.File == 0 {
		t.Fatalf("capture unreadable file metadata: identity=%+v err=%v", id, err)
	}

	socket := filepath.Join(root, "socket")
	listener, err := net.Listen("unixgram", socket)
	if err != nil {
		t.Skipf("Unix-domain socket fixture is unavailable: %v", err)
	}
	defer listener.Close()
	id, err = CaptureNativeIdentity(socket)
	if err != nil || id.File == 0 {
		t.Fatalf("capture socket metadata: identity=%+v err=%v", id, err)
	}
}
