// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func nativeLockPaths(client, path string) []string {
	if client != "dsh" {
		return nil
	}
	if filepath.Base(path) == "cordis.patch.yml" {
		root := filepath.Dir(filepath.Dir(filepath.Dir(path)))
		return []string{filepath.Join(root, "settings.yaml.lock"), filepath.Join(filepath.Dir(path), "package.json.lock")}
	}
	return []string{path + ".lock"}
}
func (d Disk) nativeLocks(paths []string) (func(), error) {
	held := []string{}
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = os.Remove(held[i])
		}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if e := safeParents(path); e != nil {
			release()
			return nil, e
		}
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			release()
			return nil, e
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			release()
			return nil, errors.New("client native writer lock is busy or unavailable; retry after the client finishes writing")
		}
		held = append(held, path)
		if e = d.Protection.Protect(path, false); e == nil {
			_, e = fmt.Fprintf(f, "%d\n", os.Getpid())
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			release()
			return nil, errors.New("cannot establish native writer lock")
		}
	}
	return release, nil
}
