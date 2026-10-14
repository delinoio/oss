// SPDX-License-Identifier: Apache-2.0
package userservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureServerOptionsCrossVolumeWindowsSymlink(t *testing.T) {
	logicalRoot := t.TempDir()
	logicalVolume := filepath.VolumeName(logicalRoot)
	var physicalRoot string
	for drive := 'C'; drive <= 'Z'; drive++ {
		volume := string(drive) + ":"
		if strings.EqualFold(volume, logicalVolume) {
			continue
		}
		candidate, err := os.MkdirTemp(volume+`\`, "delidev-tls-")
		if err == nil {
			physicalRoot = candidate
			break
		}
	}
	if physicalRoot == "" {
		t.Skip("a second writable Windows volume is unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(physicalRoot) })
	releaseA, releaseB := filepath.Join(physicalRoot, "releaseA"), filepath.Join(physicalRoot, "releaseB")
	for _, dir := range []string{releaseA, releaseB} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(logicalRoot, "current")
	if err := os.Symlink(releaseA, current); err != nil {
		t.Skipf("cross-volume directory symlinks are unavailable: %v", err)
	}
	t.Chdir(current)
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.VolumeName(original), logicalVolume) || strings.EqualFold(filepath.VolumeName(original), filepath.VolumeName(physical)) {
		t.Skip("Getwd did not retain the cross-volume logical directory")
	}
	result, err := CaptureServerOptions(ServerOptions{TLSCertificate: `\cert.pem`, TLSKey: logicalVolume + "key.pem"})
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(logicalRoot)
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(releaseB, current); err != nil {
		t.Fatal(err)
	}
	if result.TLSCertificate != logicalVolume+`\cert.pem` || result.TLSKey != filepath.Join(releaseA, "key.pem") {
		t.Fatalf("stored references changed drive or followed rotated link: %q %q", result.TLSCertificate, result.TLSKey)
	}
}
