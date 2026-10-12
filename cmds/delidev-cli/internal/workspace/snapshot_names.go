// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"sort"
	"strings"
)

// Snapshot repository roots remain UUID-indexed. Names in accepted live history
// cannot collide with another repository's UUID during copying or restoration.
func snapshotRootMap(manifest Manifest, restore bool) map[string]string {
	roots := map[string]string{}
	for _, repo := range manifest.Repositories {
		live, stored := RepositoryDirectory(repo.ID, repo.DirectoryName), string(repo.ID)
		if restore {
			roots[stored] = live
		} else {
			roots[live] = stored
		}
	}
	return roots
}

func mapSnapshotPath(path string, roots map[string]string) string {
	first, rest, child := strings.Cut(path, "/")
	if replacement, ok := roots[first]; ok {
		first = replacement
	}
	if child {
		return first + "/" + rest
	}
	return first
}

// Native walks order each directory before its children. Restore comparisons
// map paths back to UUID storage and retain that tree order in the digest.
func snapshotLayoutInventory(inventory snapshotInventory, manifest Manifest) snapshotInventory {
	if !((PrepareRequest{Repositories: manifestRepositorySpecs(manifest)}).RequiresNamedDirectories()) {
		return inventory
	}
	result := inventory
	result.Entries = append([]snapshotEntry(nil), inventory.Entries...)
	roots := snapshotRootMap(manifest, false)
	for i := range result.Entries {
		result.Entries[i].Path = mapSnapshotPath(result.Entries[i].Path, roots)
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := strings.Split(result.Entries[i].Path, "/"), strings.Split(result.Entries[j].Path, "/")
		for n := 0; n < len(a) && n < len(b); n++ {
			if a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		return len(a) < len(b)
	})
	return result
}
