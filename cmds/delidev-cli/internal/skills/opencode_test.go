// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openCodePackageFixture(t *testing.T, name, extra string) NativeSelection {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Fixture package\n"+extra+"---\nOriginal fixture instructions.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "owned.sh"), []byte("exit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, digest, err := packageFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return NativeSelection{Name: name, Path: filepath.Join(root, "SKILL.md"), ContentRevision: digest}
}
func TestOpenCodeCompleteSelectedCopiesExcludeUnselectedPackage(t *testing.T) {
	first := openCodePackageFixture(t, "first", "")
	second := openCodePackageFixture(t, "second", "disable-model-invocation: true\n")
	third := openCodePackageFixture(t, "third", "")
	home := t.TempDir()
	values, err := CopyOpenCodeRuntime(context.Background(), []NativeSelection{first, second}, home)
	if err != nil || len(values) != 2 || !values[1].ExplicitOnly {
		t.Fatalf("selected copies: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "opencode", "skills", third.Name)); !os.IsNotExist(err) {
		t.Fatal("unselected package crossed runtime boundary")
	}
	if err := os.RemoveAll(filepath.Dir(first.Path)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(second.Path)); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOpenCodeRuntime(context.Background(), home, values); err != nil {
		t.Fatal("accepted copies depended on catalog sources", err)
	}
	if err := os.Chmod(filepath.Join(filepath.Dir(values[0].Path), "scripts", "owned.sh"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyOpenCodeRuntime(context.Background(), home, values) == nil {
		t.Fatal("changed executable mode remained eligible")
	}
}
func TestOpenCodeRejectsUnsupportedMetadataAndNestedNativeDeclarations(t *testing.T) {
	for _, extra := range []string{"context: fork\n", "slash: false\n", "name: replaced\n", "disable-model-invocation: string\n"} {
		t.Run(extra, func(t *testing.T) {
			fixture := openCodePackageFixture(t, "selected", extra)
			if _, err := OpenCodePackage(context.Background(), fixture); err == nil {
				t.Fatal("unsupported metadata was accepted")
			}
		})
	}
	fixture := openCodePackageFixture(t, "selected", "")
	if err := os.Mkdir(filepath.Join(filepath.Dir(fixture.Path), "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.Path), "nested", "SKILL.md"), []byte("---\nname: foreign\ndescription: Foreign\n---\nforeign\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, fixture.ContentRevision, _ = packageFiles(context.Background(), filepath.Dir(fixture.Path))
	if _, err := OpenCodePackage(context.Background(), fixture); err == nil {
		t.Fatal("foreign native declaration remained eligible")
	}
}
func TestOpenCodeRuntimeRejectsForeignAndChangedResources(t *testing.T) {
	for _, mutation := range []string{"foreign", "bytes", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := openCodePackageFixture(t, "selected", "")
			home := t.TempDir()
			selected, err := CopyOpenCodeRuntime(context.Background(), []NativeSelection{fixture}, home)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "foreign":
				err = os.Mkdir(filepath.Join(home, "opencode", "skills", "unselected"), 0700)
			case "bytes":
				err = os.WriteFile(filepath.Join(filepath.Dir(selected[0].Path), "scripts", "owned.sh"), []byte("changed\n"), 0700)
			case "missing":
				err = os.Remove(selected[0].Path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if VerifyOpenCodeRuntime(context.Background(), home, selected) == nil {
				t.Fatal("runtime drift remained eligible")
			}
		})
	}
}
