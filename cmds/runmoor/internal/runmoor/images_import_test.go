package runmoor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type exportingTartCommand struct {
	*tartFixture
	sourceHome, archive, failure string
}

func (f *exportingTartCommand) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	if args[0] == "get" && args[1] == "external" || args[0] == "export" {
		if !strings.Contains(strings.Join(env, "\n"), "TART_HOME="+f.sourceHome+"\nTART_NO_AUTO_PRUNE=1") {
			return nil, errors.New("invalid source home")
		}
		if args[0] == "get" {
			return []byte(`{"Running":false,"State":"stopped"}`), nil
		}
		f.archive = args[2]
		if err := os.WriteFile(f.archive, []byte("private VM contents"), 0600); err != nil {
			return nil, err
		}
		if f.failure == "export" {
			return nil, errors.New("private export failure")
		}
		return nil, nil
	}
	if args[0] == "import" {
		if _, err := os.Stat(args[1]); err != nil {
			return nil, err
		}
		if f.failure == "import" {
			return nil, errors.New("private import failure")
		}
		if f.failure == "cleanup" {
			if err := os.Remove(f.archive); err != nil {
				return nil, err
			}
			if err := os.Mkdir(f.archive, 0700); err != nil {
				return nil, err
			}
		}
	}
	return f.tartFixture.Run(ctx, name, args, env, in)
}

func TestLocalImportCleansExportAndReportsCleanupFailure(t *testing.T) {
	for _, failure := range []string{"", "export", "import", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			command := &exportingTartCommand{tartFixture: fixture, sourceHome: t.TempDir(), failure: failure}
			driver.Exec = command
			images := &ImageManager{Store: s, Tart: driver}
			_, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", From: "external", SourceHome: command.sourceHome, Resources: Resources{1, 512}})
			if (err != nil) != (failure != "") {
				t.Fatalf("unexpected import result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("export contents or raw diagnostics leaked")
			}
			if failure == "cleanup" {
				requireCode(t, err, ErrOwnership)
				for _, im := range s.View().Images {
					requireCode(t, im.Problem, ErrOwnership)
				}
			} else if _, err := os.Lstat(command.archive); !os.IsNotExist(err) {
				t.Fatal("temporary export survived completed create/import")
			}
		})
	}
}

func TestRestartCleansOnlyJournaledImageExports(t *testing.T) {
	c, s := fixtureStore(t)
	driver, _ := fakeTart(c)
	var ids []string
	for _, phase := range []ImagePhase{ImageOpen, ImagePreparing, ImageSealed, ImageRemoving} {
		id := newID()
		ids = append(ids, id)
		if err := s.Update(func(v *Snapshot) error {
			v.Images[id] = &Image{ID: id, VM: "rm-image-" + id, Phase: phase}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(importArchivePath(c, id), []byte("interrupted export"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	unowned := importArchivePath(c, newID())
	if err := os.WriteFile(unowned, []byte("unowned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	images := &ImageManager{Store: reopened, Tart: driver}
	for i := 0; i < 2; i++ {
		if err = images.Reconcile(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if _, err := os.Lstat(importArchivePath(c, id)); !os.IsNotExist(err) {
			t.Fatal("restart left a journaled export behind")
		}
	}
	if data, err := os.ReadFile(unowned); err != nil || string(data) != "unowned" {
		t.Fatal("reconciliation removed an unowned export")
	}
}

func TestImageRemovalRetainsJournalUntilExportCleanupCompletes(t *testing.T) {
	c, s := fixtureStore(t)
	driver, _ := fakeTart(c)
	images := &ImageManager{Store: s, Tart: driver}
	im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
	if err != nil {
		t.Fatal(err)
	}
	archive := importArchivePath(c, im.ID)
	outside := filepath.Join(t.TempDir(), "operator.tvm")
	if err = os.WriteFile(outside, []byte("operator source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, archive); err != nil {
		t.Fatal(err)
	}
	requireCode(t, images.Reconcile(context.Background(), c), ErrOwnership)
	_, err = images.Operate(context.Background(), c, ImageRequest{Action: "remove", ID: im.ID})
	requireCode(t, err, ErrOwnership)
	if got := s.View().Images[im.ID]; got == nil || got.Phase != ImageRemoving || got.Problem == nil {
		t.Fatal("cleanup failure discarded its retry journal")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "operator source" {
		t.Fatal("cleanup followed an unrelated symlink")
	}
	if err = os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archive, []byte("owned export"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = images.Operate(context.Background(), c, ImageRequest{Action: "remove", ID: im.ID}); err != nil {
		t.Fatal(err)
	}
	if s.View().Images[im.ID] != nil {
		t.Fatal("successful retry retained the removed image")
	}
	if _, err = os.Lstat(archive); !os.IsNotExist(err) {
		t.Fatal("image removal left its export archive")
	}
}
