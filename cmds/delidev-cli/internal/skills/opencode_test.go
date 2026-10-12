// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
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

func TestOpenCodeInventoryExcludesAmbientHostPackages(t *testing.T) {
	ctx := context.Background()
	server, worker := domain.NewID(), domain.NewID()
	manager := Manager{Root: t.TempDir(), Home: t.TempDir()}
	scope := domain.SkillReadRequest{Harness: domain.OpenCode, ManagedServerID: server, WorkerDeviceID: worker}
	for _, name := range []string{"first", "second"} {
		source := openCodePackageFixture(t, name, "")
		files, digest, err := packageFiles(ctx, filepath.Dir(source.Path))
		if err != nil {
			t.Fatal(err)
		}
		id := domain.NewID()
		target := filepath.Join(managedRoot(manager.Root, server, worker), "retained", string(id))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := writePackage(ctx, files, digest, target); err != nil {
			t.Fatal(err)
		}
		scope.ManagedPackages = append(scope.ManagedPackages, domain.ManagedSkillPackage{ID: id, WorkerDeviceID: worker, Revision: 1, ContentRevision: digest, Name: name, Description: "Fixture package", Provenance: "worker-import", Enabled: true})
	}
	ambient := openCodePackageFixture(t, "ambient", "")
	files, ambientDigest, _ := packageFiles(ctx, filepath.Dir(ambient.Path))
	target := filepath.Join(manager.Home, ".agents", "skills", "ambient")
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writePackage(ctx, files, ambientDigest, target); err != nil {
		t.Fatal(err)
	}
	inventory, err := manager.List(ctx, scope, []string{manager.Home})
	if err != nil || len(inventory.Entries) != 2 {
		t.Fatalf("selected-only inventory: %#v %v", inventory, err)
	}
	for _, entry := range inventory.Entries {
		if entry.Name == "ambient" {
			t.Fatal("host skill was offered to native OpenCode")
		}
	}
}
func TestOpenCodeRejectsDuplicateNativeNamesBeforeCopy(t *testing.T) {
	first := openCodePackageFixture(t, "same", "")
	second := openCodePackageFixture(t, "same", "")
	if _, err := CopyOpenCodeRuntime(context.Background(), []NativeSelection{first, second}, t.TempDir()); err == nil {
		t.Fatal("duplicate native names were copied")
	}
}

func TestOpenCodeIndependentForkCopySurvivesParentRemoval(t *testing.T) {
	source := openCodePackageFixture(t, "selected", "")
	parent := t.TempDir()
	selected, err := CopyOpenCodeRuntime(context.Background(), []NativeSelection{source}, parent)
	if err != nil {
		t.Fatal(err)
	}
	child := t.TempDir()
	copied, err := CopyOpenCodeRuntime(context.Background(), []NativeSelection{selected[0].NativeSelection}, child)
	if err != nil {
		t.Fatal(err)
	}
	if copied[0].Path == selected[0].Path || copied[0].ContentRevision != selected[0].ContentRevision {
		t.Fatal("Fork copy lost independent package ownership")
	}
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOpenCodeRuntime(context.Background(), child, copied); err != nil {
		t.Fatal("parent cleanup removed child package authority", err)
	}
}
