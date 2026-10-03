// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import "os"

func snapshotSymlinkKind(os.FileInfo) (snapshotLinkKind, error) { return "", nil }
func createSnapshotSymlink(link, target string, kind snapshotLinkKind) error {
	return os.Symlink(link, target)
}
