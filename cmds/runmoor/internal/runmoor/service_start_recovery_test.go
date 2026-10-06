package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Model the target replacement honoring Stop while the reload CLI still owns
// recovery. Its successful exit leaves a loaded but inactive native service.
func completedReloadStopFixture(t *testing.T, platform string) (*reloadFixture, *serviceReloadJournal) {
	t.Helper()
	f := newReloadFixture(t, platform)
	f.onCommand = func(command string) error {
		if strings.Contains(command, " --signal=SIGKILL ") || strings.Contains(command, " bootstrap ") {
			return errors.New("interrupted native replacement")
		}
		return nil
	}
	if err := f.reload(); err == nil {
		t.Fatal("reload interruption was not retained")
	}
	if err := f.m.Stop(false); err != nil {
		t.Fatal(err)
	}
	f.replaceManager()
	f.loaded = true
	f.pid = os.Getpid()
	f.args[f.pid] = f.args[303]
	committed, preserve, j, err := f.r.startup(context.Background(), f.path, f.c)
	if err != nil || !preserve || j == nil || f.r.reloadInitiatorFinished(j) {
		t.Fatalf("immediate replacement lost live reload authority: %v", err)
	}
	f.m.PreserveStop = preserve
	if err := f.m.initializeRun(committed); err != nil {
		t.Fatal(err)
	}
	if !f.m.Store.View().Stopping || !f.m.readyToStop() {
		t.Fatal("immediate replacement undid completed Stop")
	}
	f.pid, f.peer = 0, 0
	f.onCommand = nil
	f.commands = nil
	return f, j
}

func explicitFixtureStart(t *testing.T, f *reloadFixture) error {
	t.Helper()
	lock, err := lockServiceOperation(f.r.Unit)
	if err != nil {
		return err
	}
	defer unlockState(lock)
	return f.r.start(context.Background(), f.path, f.c)
}

func finishFixtureReload(f *reloadFixture, j *serviceReloadJournal) {
	prior := f.r.ProcessStart
	f.r.ProcessStart = func(pid int) (string, error) {
		if pid == j.ReloadPID {
			return "", os.ErrNotExist
		}
		return prior(pid)
	}
}

func TestServiceStartResumesCompletedReloadStopOnFirstAttempt(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		for _, finished := range []string{"exited", "pid_reused"} {
			t.Run(platform+"/"+finished, func(t *testing.T) {
				f, j := completedReloadStopFixture(t, platform)
				finishFixtureReload(f, j)
				f.pid = os.Getpid()
				_, preserveAutomatic, _, err := f.r.startup(context.Background(), f.path, f.c)
				if err != nil || !preserveAutomatic {
					t.Fatalf("initiator exit cleared automatic replacement Stop: %v", err)
				}
				f.pid = 0
				pool := sortedPools(f.m.Store.View())[0].ID
				if err := f.m.Store.Update(func(s *Snapshot) error {
					s.Paused = true
					s.Pools[pool].Phase = Paused
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.m.Store.Close(); err != nil {
					t.Fatal(err)
				}
				finishFixtureReload(f, j)
				if finished == "pid_reused" {
					f.r.ProcessStart = func(int) (string, error) { return "different:process", nil }
				}
				spawned := false
				f.onCommand = func(command string) error {
					if strings.Contains(command, " enable --now ") || strings.Contains(command, " bootstrap ") {
						if journal, err := readReloadJournal(f.r.Unit); err != nil || journal != nil {
							t.Fatalf("obsolete intent survived to native Start: %v", err)
						}
						spawned = true
						f.replaceManager()
						f.loaded = true
					}
					return nil
				}
				if err := explicitFixtureStart(t, f); err != nil {
					t.Fatal(err)
				}
				if !spawned {
					t.Fatal("explicit Start did not spawn the target manager")
				}
				f.pid = os.Getpid()
				f.args[f.pid] = f.args[303]
				committed, preserve, recovery, err := f.r.startup(context.Background(), f.path, f.c)
				if err != nil || preserve || recovery != nil {
					t.Fatalf("first explicit Start retained Stop authority: %v", err)
				}
				store, err := OpenStore(committed)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				f.m.Store, f.m.PreserveStop = store, preserve
				if err := f.m.initializeRun(committed); err != nil {
					t.Fatal(err)
				}
				state := store.View()
				if state.Stopping || f.m.readyToStop() || !state.Paused || state.Pools[pool].Phase != Paused {
					t.Fatal("first Start failed to resume or changed pause authority")
				}
				if !strings.Contains(f.log.String(), "service_start_completed_stop_recovered") {
					t.Fatal("completed Stop recovery was not logged")
				}
			})
		}
	}
}

func TestServiceStartPreservesUncertainReloadStop(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		for _, scenario := range []string{
			"live_initiator", "legacy_initiator", "unreadable_initiator", "empty_initiator",
			"active_service", "native_probe_failure", "state_owned", "not_stopping", "runner_cleanup",
			"image_cleanup", "image_preparation", "artifact_cleanup", "artifact_reservation", "host_cleanup",
			"installation", "storage", "committed_storage", "configuration", "definition_content", "definition_identity",
			"definition_replaced_during_probe", "journal_replaced_during_probe", "service_started_during_probe",
		} {
			t.Run(platform+"/"+scenario, func(t *testing.T) {
				f, j := completedReloadStopFixture(t, platform)
				finishFixtureReload(f, j)
				if err := f.m.Store.Update(func(s *Snapshot) error {
					switch scenario {
					case "not_stopping":
						s.Stopping = false
					case "runner_cleanup":
						id := newID()
						s.Runners[id] = &Runner{ID: id, Phase: Cleaning, Terminated: true, Resources: Resources{1, 128}}
					case "image_cleanup":
						s.Images[newID()] = &Image{Phase: ImageRemoving}
					case "image_preparation":
						s.Images[newID()] = &Image{Phase: ImagePreparing}
					case "artifact_cleanup":
						s.Artifacts[newID()] = &RunnerArtifact{Phase: ArtifactRemoving}
					case "artifact_reservation":
						s.Artifacts[newID()] = &RunnerArtifact{Phase: ArtifactPreparing, Reserved: true, Resources: Resources{1, 128}}
					case "host_cleanup":
						s.HostExecutions[newID()] = &HostExecution{Terminated: true}
					case "installation":
						s.Installation = newID()
					case "committed_storage":
						s.Requested.Storage.Data += "-different"
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				beforeState := fingerprint(f.m.Store.View())
				if scenario != "state_owned" {
					if err := f.m.Store.Close(); err != nil {
						t.Fatal(err)
					}
				}
				switch scenario {
				case "live_initiator":
					f.r.ProcessStart = func(int) (string, error) { return j.ReloadStart, nil }
				case "legacy_initiator":
					j.ReloadPID, j.ReloadStart = 0, ""
					writeFixtureJournal(t, f, j)
				case "unreadable_initiator":
					f.r.ProcessStart = func(int) (string, error) { return "", os.ErrPermission }
				case "empty_initiator":
					f.r.ProcessStart = func(int) (string, error) { return "", nil }
				case "active_service":
					f.pid = 303
				case "native_probe_failure":
					f.onCommand = func(string) error { return os.ErrPermission }
				case "storage":
					f.c.Storage.Data += "-different"
				case "configuration":
					f.path += "-different"
				case "definition_content":
					definition, err := serviceDefinition(platform, "/foreign/runmoor", f.path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f.r.Unit, []byte(definition), 0600); err != nil {
						t.Fatal(err)
					}
				case "definition_identity":
					replaceFixtureDefinition(t, f)
				case "definition_replaced_during_probe", "journal_replaced_during_probe", "service_started_during_probe":
					probes := 0
					f.onCommand = func(command string) error {
						if strings.Contains(command, " show ") || command == "launchctl list" {
							probes++
							if probes == 2 {
								switch scenario {
								case "definition_replaced_during_probe":
									replaceFixtureDefinition(t, f)
								case "journal_replaced_during_probe":
									j.Token = newID()
									writeFixtureJournal(t, f, j)
								case "service_started_during_probe":
									f.pid = 303
								}
							}
						}
						return nil
					}
				}
				if err := explicitFixtureStart(t, f); err == nil {
					t.Fatal("uncertain recovery authorized Start")
				}
				if journal, err := readReloadJournal(f.r.Unit); err != nil || journal == nil {
					t.Fatalf("uncertain recovery intent was removed: %v", err)
				}
				if f.mutated() || strings.Contains(strings.Join(f.commands, "\n"), " enable ") {
					t.Fatalf("uncertain recovery changed the native service: %v", f.commands)
				}
				state, err := ReadSnapshot(Config{Storage: j.Storage})
				if err != nil || fingerprint(state) != beforeState {
					t.Fatalf("uncertain recovery changed cleanup or reservations: %v", err)
				}
			})
		}
	}
}

func TestServiceStartWithoutReloadJournalClearsCompletedStop(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			f := newReloadFixture(t, platform)
			if err := f.m.Stop(false); err != nil {
				t.Fatal(err)
			}
			if err := f.m.Store.Close(); err != nil {
				t.Fatal(err)
			}
			f.pid, f.peer = 0, 0
			if err := explicitFixtureStart(t, f); err != nil {
				t.Fatal(err)
			}
			_, preserve, recovery, err := f.r.startup(context.Background(), f.path, f.c)
			if err != nil || preserve || recovery != nil {
				t.Fatalf("ordinary Start changed: %v", err)
			}
			store, err := OpenStore(f.c)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			f.m.Store, f.m.PreserveStop = store, preserve
			if err := f.m.initializeRun(f.c); err != nil {
				t.Fatal(err)
			}
			if store.View().Stopping || f.m.readyToStop() {
				t.Fatal("ordinary Start did not clear completed Stop")
			}
		})
	}
}

func writeFixtureJournal(t *testing.T, f *reloadFixture, j *serviceReloadJournal) {
	t.Helper()
	body, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reloadJournalPath(f.r.Unit), body, 0600); err != nil {
		t.Fatal(err)
	}
}

func replaceFixtureDefinition(t *testing.T, f *reloadFixture) {
	t.Helper()
	body, err := os.ReadFile(f.r.Unit)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(f.r.Unit), "replacement")
	if err := os.WriteFile(replacement, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, f.r.Unit); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(f.r.Unit)
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatal("definition fixture did not preserve contents")
	}
}
