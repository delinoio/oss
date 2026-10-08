// SPDX-License-Identifier: Apache-2.0
package runmoor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHostClosedTartImagesPermitDrainedBackup(t *testing.T) {
	for _, closed := range []bool{false, true} {
		for _, relocated := range []bool{false, true} {
			t.Run(map[bool]string{false: "created", true: "closed"}[closed]+"/"+map[bool]string{false: "restore", true: "relocate"}[relocated], func(t *testing.T) {
				c, _, p, before := hostDrainedBackupFixture(t)
				store, err := OpenStore(c)
				if err != nil {
					t.Fatal(err)
				}
				tart, _ := fakeTart(c)
				manager := ImageManager{Store: store, Tart: tart}
				image, err := manager.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "closed", IPSW: "latest", Resources: Resources{CPU: 1, MemoryMiB: 512}})
				if err != nil {
					t.Fatal(err)
				}
				if closed {
					if _, err = manager.Operate(context.Background(), c, ImageRequest{Action: "open", ID: image.ID}); err != nil {
						t.Fatal(err)
					}
					if _, err = manager.Operate(context.Background(), c, ImageRequest{Action: "close", ID: image.ID}); err != nil {
						t.Fatal(err)
					}
				}
				state := store.View()
				if state.Images[image.ID].Phase != ImagePreparing || !state.Images[image.ID].CreationComplete || !hostRestoreBoundary(state, c) {
					t.Fatal("closed creation was not settled")
				}
				if err = store.Close(); err != nil {
					t.Fatal(err)
				}
				original := c
				if relocated {
					c.Storage = fixtureConfig(t).Storage
					copyHostBackupData(t, original.Storage.State, c.Storage.State)
				} else {
					original.Storage.Data += "-backup"
					if err = os.Rename(c.Storage.Data, original.Storage.Data); err != nil {
						t.Fatal(err)
					}
				}
				copyHostBackupData(t, original.Storage.Data, c.Storage.Data)
				_, err = openHostDirectory(c, before, before.Installation, false)
				requireCode(t, err, ErrOwnership)
				store, err = OpenStore(c)
				if err != nil {
					t.Fatal("closed image blocked paired backup", err)
				}
				defer store.Close()
				after := *store.View().HostDirectories[p.Image]
				if after.Identity == before.Identity || after.Token != before.Token || after.Digest != before.Digest {
					t.Fatal("invalid host identity rebind")
				}
				root, err := openHostDirectory(c, after, after.Installation, false)
				if err != nil {
					t.Fatal(err)
				}
				root.Close()
			})
		}
	}
}

func TestHostClosedImageRestoreRejectsUnsettledOwnership(t *testing.T) {
	for _, scenario := range []string{"incomplete", "open", "removing", "PID", "start", "alias", "foreign alias", "destination alias", "reserved artifact", "removing artifact"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, p, before := hostDrainedBackupFixture(t)
			store, err := OpenStore(c)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			id := newID()
			if err = store.Update(func(s *Snapshot) error {
				s.Images[id] = &Image{ID: id, Phase: ImagePreparing, CreationComplete: true}
				switch scenario {
				case "incomplete":
					s.Images[id].CreationComplete = false
				case "open":
					s.Images[id].Phase = ImageOpen
				case "removing":
					s.Images[id].Phase = ImageRemoving
				case "PID":
					s.ImageTartPIDs = map[string]int{id: 123}
				case "start":
					s.ImageTartStarts = map[string]string{id: "unresolved"}
				case "reserved artifact":
					s.Artifacts[p.Image].Reserved = true
				case "removing artifact":
					s.Artifacts[p.Image].Phase = ArtifactRemoving
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			destination := c
			if scenario == "destination alias" {
				destination.Storage.Data = c.Storage.Data + "-destination"
			}
			if scenario == "alias" || scenario == "foreign alias" || scenario == "destination alias" {
				alias := filepath.Join(destination.Storage.Data, "tart", "vms", tartRunAlias(id))
				if err = os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
					t.Fatal(err)
				}
				if scenario == "foreign alias" {
					err = os.WriteFile(alias, []byte("foreign"), 0600)
				} else {
					err = os.Symlink("/dev/fd/3", alias)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if hostRestoreBoundary(store.View(), destination) {
				t.Fatal("unsettled ownership admitted rebind")
			}
			requireCode(t, rebindHostDistributions(context.Background(), store, destination), ErrOwnership)
			if store.View().HostDirectories[p.Image].Identity != before.Identity {
				t.Fatal("rejected restore changed durable identity")
			}
		})
	}
}
