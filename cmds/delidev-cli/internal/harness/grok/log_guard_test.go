// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeLogGuardRefusesExistingAndChangedPaths(t *testing.T) {
	for _, kind := range []string{"directory", "file", "link"} {
		t.Run(kind, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, "valid")
			path := filepath.Join(config.Probe.Home, "logs")
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0700)
			case "file":
				err = os.WriteFile(path, nil, 0600)
			case "link":
				err = os.Symlink(filepath.Join(config.Probe.Home, "foreign"), path)
			}
			if err != nil {
				t.Skipf("fixture creation unavailable: %v", err)
			}
			if createNativeLogGuard(config.Probe.Home) == nil {
				t.Fatal("native logging guard adopted a preexisting path")
			}
		})
	}
	config, _ := fixtureAPIConfig(t, "valid")
	if err := createNativeLogGuard(config.Probe.Home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.Probe.Home, "logs")
	if checkNativeLogGuard(path) != nil || os.MkdirAll(filepath.Join(path, "nested"), 0700) == nil {
		t.Fatal("native logging remained writable")
	}
	if err := os.WriteFile(path, []byte("fixture-native-log-content"), 0600); err != nil {
		t.Fatal(err)
	}
	if checkNativeLogGuard(path) == nil {
		t.Fatal("changed native logging guard retained authority")
	}
}
