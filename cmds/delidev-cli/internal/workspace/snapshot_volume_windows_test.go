//go:build windows

// SPDX-License-Identifier: Apache-2.0
package workspace

import "testing"

func TestSnapshotExternalGitStoreAcrossWindowsVolumes(t *testing.T) {
	for _, c := range []struct {
		root, path string
		external   bool
	}{
		{`C:\worker\workspaces\session`, `D:\repositories\repo\.git`, true},
		{`C:\worker\workspaces\session`, `c:\worker\workspaces\session\repo\.git`, false},
		{`C:\worker\workspaces\session`, `C:\repositories\repo\.git`, true},
		{`\\server\one\worker`, `\\server\two\git`, true},
	} {
		external, err := snapshotExternalGitStore(c.root, c.path)
		if err != nil || external != c.external {
			t.Fatal(c, external, err)
		}
	}
}
