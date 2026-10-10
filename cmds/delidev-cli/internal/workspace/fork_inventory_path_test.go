// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalForkGitDirectoryKeepsOriginalNativeIdentity(t *testing.T) {
	original, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitPath := original
	if runtime.GOOS == "windows" {
		gitPath = filepath.ToSlash(original)
	}
	canonical, err := canonicalForkGitDirectory(gitPath)
	if err != nil || canonical != original {
		t.Fatal("Git path did not retain its canonical original directory", err)
	}
	if _, err := directoryIdentityDigest(canonical); err != nil {
		t.Fatal("canonical Git directory failed strict identity capture", err)
	}
	if _, err := canonicalForkGitDirectory("relative"); err == nil {
		t.Fatal("relative Git administration acquired identity")
	}
	file := filepath.Join(original, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalForkGitDirectory(file); err == nil {
		t.Fatal("regular file acquired directory identity")
	}
	alias := filepath.Join(original, "alias")
	if err := os.Symlink(original, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Log("symlink creation unavailable; native alias cases remain covered separately")
			return
		}
		t.Fatal(err)
	}
	if _, err := canonicalForkGitDirectory(alias); err == nil {
		t.Fatal("linked Git administration acquired original identity")
	}
}
