package runmoor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func copyHostBackupData(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		out := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(out, 0700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, out)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, body, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hostDrainedBackupFixture(t *testing.T) (Config, *fixtureHost, Pool, HostDirectory) {
	t.Helper()
	c, store, native, p := hostFixture(t)
	p = buildHostFixture(t, c, store, native, p)
	d := *store.View().HostDirectories[p.Image]
	if err := store.Update(func(s *Snapshot) error {
		s.Config = c
		s.Requested = c
		s.Stopping = true
		s.Artifacts[p.Image].Phase = ArtifactReady
		s.Artifacts[p.Image].Reserved = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return c, native, p, d
}

func TestHostDrainedBackupRestoreAndRelocation(t *testing.T) {
	for _, relocate := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore", true: "relocate"}[relocate], func(t *testing.T) {
			c, native, p, before := hostDrainedBackupFixture(t)
			original := c
			if relocate {
				c.Storage = fixtureConfig(t).Storage
				if err := privateDir(c.Storage.State); err != nil {
					t.Fatal(err)
				}
				body, err := os.ReadFile(filepath.Join(original.Storage.State, "state.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(c.Storage.State, "state.sqlite"), body, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				original.Storage.Data += "-backup"
				if err := os.Rename(c.Storage.Data, original.Storage.Data); err != nil {
					t.Fatal(err)
				}
			}
			copyHostBackupData(t, original.Storage.Data, c.Storage.Data)
			state, err := ReadSnapshot(c)
			if err != nil || *state.HostDirectories[p.Image] != before {
				t.Fatal("read-only inspection changed the backup", err)
			}
			_, err = openHostDirectory(c, before, before.Installation, false)
			requireCode(t, err, ErrOwnership)
			store, err := OpenStore(c)
			if err != nil {
				t.Fatal("paired backup could not be restored", err)
			}
			after := *store.View().HostDirectories[p.Image]
			if after.Identity == before.Identity || after.Token != before.Token || after.Installation != before.Installation || after.Version != before.Version || after.Digest != before.Digest {
				t.Fatal("restore changed portable ownership or retained the copied inode")
			}
			driver := HostDriver{Store: store, Native: native}
			if err := driver.Validate(context.Background(), c, p, store.View()); err != nil {
				t.Fatal("restored distribution is unusable", err)
			}
			store.Close()
			store, err = OpenStore(c)
			if err != nil {
				t.Fatal("restore was not restart-safe", err)
			}
			defer store.Close()
			if err := driver.Validate(context.Background(), c, p, store.View()); err != nil {
				t.Fatal("rebound marker did not match durable state", err)
			}
		})
	}
}

func TestHostBackupRebindingRejectsChangedOrLiveResources(t *testing.T) {
	for _, scenario := range []string{"token", "version", "digest", "symlink", "busy", "reserved", "uncleaned", "live restart", "marker before state"} {
		t.Run(scenario, func(t *testing.T) {
			c, native, p, before := hostDrainedBackupFixture(t)
			store, err := OpenStore(c)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			backup := c.Storage.Data + "-backup"
			if err := os.Rename(c.Storage.Data, backup); err != nil {
				t.Fatal(err)
			}
			copyHostBackupData(t, backup, c.Storage.Data)
			path := hostDirectoryPath(c, before)
			switch scenario {
			case "token", "version", "marker before state":
				marker := before
				if scenario == "token" {
					marker.Token = newID()
				} else if scenario == "version" {
					marker.Version = "9.9.9"
				} else {
					info, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					marker.Identity = hostFileIdentity(info)
				}
				root, err := os.OpenRoot(path)
				if err != nil {
					t.Fatal(err)
				}
				err = hostRootWrite(root, hostOwnerFile, marker)
				root.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "digest":
				if err := os.WriteFile(filepath.Join(path, "runner/foreign"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+"-foreign"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-foreign", path); err != nil {
					t.Fatal(err)
				}
			default:
				if err := store.Update(func(s *Snapshot) error {
					switch scenario {
					case "busy":
						s.Runners[newID()] = &Runner{Phase: Busy}
					case "reserved":
						s.Artifacts[p.Image].Reserved = true
					case "uncleaned":
						s.HostExecutions[newID()] = &HostExecution{Terminated: true}
					case "live restart":
						s.Stopping = false
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "live restart" {
				store.Close()
				store, err = OpenStore(c)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if store.View().HostDirectories[p.Image].Identity != before.Identity {
					t.Fatal("live restart adopted a replaced distribution")
				}
				driver := HostDriver{Native: native}
				requireCode(t, driver.Validate(context.Background(), c, p, store.View()), ErrOwnership)
				return
			}
			err = rebindHostDistributions(context.Background(), store, c)
			if scenario == "marker before state" {
				if err != nil || store.View().HostDirectories[p.Image].Identity == before.Identity {
					t.Fatal("interrupted rebind did not recover", err)
				}
			} else {
				requireCode(t, err, ErrOwnership)
				if store.View().HostDirectories[p.Image].Identity != before.Identity {
					t.Fatal("failed recovery rewrote ownership")
				}
			}
		})
	}
}
