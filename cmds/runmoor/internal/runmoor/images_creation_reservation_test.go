package runmoor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type interruptedImageCreate struct{ *tartFixture }

func (f interruptedImageCreate) Run(ctx context.Context, name string, args, env []string, in io.Reader) ([]byte, error) {
	b, err := f.tartFixture.Run(ctx, name, args, env, in)
	if err == nil && len(args) > 0 && (args[0] == "create" || args[0] == "import") {
		return nil, errors.New("private upstream create failure")
	}
	return b, err
}
func assertImageCreationReservation(t *testing.T, s Snapshot, id string) {
	t.Helper()
	im := s.Images[id]
	used, count, vms := usage(s)
	if im == nil || im.Phase != ImageOpen || used != (Resources{1, 512}) || count != 1 || vms != 1 {
		t.Fatalf("creation reservation lost: image=%+v usage=%+v count=%d vms=%d", im, used, count, vms)
	}
	if im.Problem == nil || strings.Contains(im.Problem.Error(), "private upstream") {
		t.Fatal("missing or unsafe recovery diagnosis")
	}
}
func TestInterruptedImageCreationRetainsMarkerlessReservation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix Tart ownership fixtures")
	}
	for _, action := range []string{"create", "import"} {
		t.Run(action, func(t *testing.T) {
			c, s := fixtureStore(t)
			driver, fixture := fakeTart(c)
			driver.Exec = interruptedImageCreate{fixture}
			images := &ImageManager{Store: s, Tart: driver}
			req := ImageRequest{Action: "create", Name: "setup", Resources: Resources{1, 512}}
			if action == "create" {
				req.IPSW = "/fixture.ipsw"
			} else {
				req.From = "/fixture.tvm"
			}
			_, operationErr := images.Operate(context.Background(), c, req)
			if len(s.View().Images) != 1 {
				t.Fatal("interrupted creation lost its record")
			}
			for id, im := range s.View().Images {
				if _, err := os.Lstat(tartCreationVMPath(c, id)); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(vmPath(c, im.VM)); !os.IsNotExist(err) {
					t.Fatalf("canonical path changed: %v", err)
				}
				assertImageCreationReservation(t, s.View(), id)
				requireCode(t, operationErr, ErrOwnership)
				requireCode(t, images.Reconcile(context.Background(), c), ErrOwnership)
				assertImageCreationReservation(t, s.View(), id)
			}
		})
	}
}
func TestImageFailureReconcilesCreationStageBeforeReleasingCapacity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix Tart ownership fixtures")
	}
	for _, scenario := range []string{"markerless", "marked", "foreign marker", "operation lock", "unsafe stage", "absent"} {
		t.Run(scenario, func(t *testing.T) {
			c, s := fixtureStore(t)
			id := newID()
			im := &Image{ID: id, Name: "setup", VM: "rm-image-" + id, Phase: ImageOpen, Resources: Resources{1, 512}}
			if err := s.Update(func(v *Snapshot) error { v.Images[id] = im; return nil }); err != nil {
				t.Fatal(err)
			}
			if err := claimVM(c, im.VM, s.View().Installation, id); err != nil {
				t.Fatal(err)
			}
			home, lock, err := prepareTartCreationHome(c, id)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if scenario != "absent" && scenario != "operation lock" {
				createFixtureTartVM(t, c, home, creationVMName(id))
				if scenario == "marked" || scenario == "foreign marker" {
					if err = publishVMOwnerMarkerAt(c, tartCreationVMPath(c, id), im.VM, s.View().Installation, id); err != nil {
						t.Fatal(err)
					}
					if scenario == "foreign marker" {
						if err = os.WriteFile(filepath.Join(tartCreationVMPath(c, id), vmOwnerMarkerName), []byte("foreign marker"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "unsafe stage" {
					if err = os.Chmod(tartCreationVMPath(c, id), 0777); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario != "operation lock" {
				if err = lock.Close(); err != nil {
					t.Fatal(err)
				}
			}
			driver, fixture := fakeTart(c)
			images := &ImageManager{Store: s, Tart: driver}
			err = images.imageFailure(id, errors.New("private upstream failure"))
			if scenario == "absent" {
				used, count, vms := usage(s.View())
				if used != (Resources{}) || count != 0 || vms != 0 || s.View().Images[id].Phase != ImagePreparing {
					t.Fatal("confirmed absent stage retained capacity")
				}
				return
			}
			assertImageCreationReservation(t, s.View(), id)
			if scenario == "marked" {
				if err = verifyVMOwner(c, im.VM, s.View().Installation, id); err != nil {
					t.Fatal(err)
				}
				fixture.running[im.VM] = true
			} else if scenario != "operation lock" {
				if _, err = os.Lstat(tartCreationVMPath(c, id)); err != nil {
					t.Fatal("uncertain stage removed", err)
				}
			}
			_ = images.Reconcile(context.Background(), c)
			assertImageCreationReservation(t, s.View(), id)
		})
	}
}
