// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/cachecow/internal/preset"
	"github.com/delinoio/oss/cmds/cachecow/internal/session"
	"github.com/delinoio/oss/cmds/cachecow/internal/state"
)

func TestWriterInvalidatesOverlappingProvenance(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "equal", true: "ancestor"}[nested], func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			old := preset.Result{Roots: []preset.Root{{Path: target}, {Path: filepath.Join(root, "build-a")}}}
			changedTarget := target
			if nested {
				changedTarget = filepath.Join(target, "nested")
			}
			current := preset.Result{Roots: []preset.Root{{Path: changedTarget}, {Path: filepath.Join(root, "build-b")}}}
			unrelated := preset.Result{Roots: []preset.Root{{Path: filepath.Join(root, "unrelated")}}}
			for _, r := range []preset.Result{old, unrelated} {
				if err := state.Write(metadataPath(root, r), provenance{SchemaVersion: 1, Paths: paths(r)}); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := json.Marshal(claims{Current: current})
			p := session.Plan{Root: root, Action: session.Run, Claims: data}
			hooks := Records()
			if err := hooks.Before(p); err != nil {
				t.Fatal(err)
			}
			if err := hooks.After(p, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(metadataPath(root, old)); !os.IsNotExist(err) {
				t.Fatalf("stale successful provenance survived: %v", err)
			}
			if _, err := os.Stat(metadataPath(root, unrelated)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareRequiresSupervisorCloneEvidence(t *testing.T) {
	root := t.TempDir()
	current := preset.Result{Roots: []preset.Root{{Path: filepath.Join(root, "cache")}}}
	data, _ := json.Marshal(claims{Current: current})
	p := session.Plan{Root: root, Action: session.Prepare, Claims: data}
	if err := Records().After(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metadataPath(root, current)); !os.IsNotExist(err) {
		t.Fatal("retained cache acquired provenance")
	}
	p.PreparedAll = true
	if err := Records().After(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metadataPath(root, current)); err != nil {
		t.Fatal(err)
	}
}
