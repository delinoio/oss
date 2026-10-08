package runmoor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestServiceStopActiveAuthority(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		for _, scenario := range []string{"changed storage", "foreign peer", "unavailable status", "native replacement", "PID reuse", "native inspection failure", "matching legacy peer"} {
			t.Run(platform+"/"+scenario, func(t *testing.T) {
				f := newReloadFixture(t, platform)
				definition, err := os.ReadFile(f.r.Unit)
				if err != nil {
					t.Fatal(err)
				}
				identity, err := f.r.admitServiceManager(context.Background(), definition)
				if err != nil {
					t.Fatal(err)
				}
				pool := sortedPools(f.m.Store.View())[0].ID
				runner := seedRunner(t, f.m, pool, Busy)
				before := f.m.Store.View().Runners[runner]
				candidate := f.c
				calls := 0
				f.r.Control = func(ctx context.Context, c Config, req ControlRequest, expected int) (ControlResponse, int, error) {
					calls++
					if expected != identity.pid {
						t.Fatal("control lost admitted PID")
					}
					if scenario == "changed storage" || scenario == "foreign peer" {
						return ControlResponse{}, 404, errors.New("private peer failure")
					}
					if req.Action == "stop" {
						if err := f.m.Stop(false); err != nil {
							t.Fatal(err)
						}
						switch scenario {
						case "native replacement":
							f.pid = 404
						case "PID reuse":
							f.r.ProcessStart = func(int) (string, error) { return "recycled", nil }
						case "native inspection failure":
							f.onCommand = func(string) error { return errors.New("private native failure") }
						}
					}
					if scenario == "unavailable status" && calls > 2 {
						return ControlResponse{}, 0, errors.New("private socket failure")
					}
					response := ControlResponse{SchemaVersion: 1, Status: statusOf(f.m.Store.View(), true)}
					if scenario == "matching legacy peer" && req.Action == "status" && calls > 2 {
						response.Status.Runners = nil
					}
					return response, identity.pid, nil
				}
				if scenario == "changed storage" {
					candidate.Storage.State = filepath.Join(t.TempDir(), "new-state")
					candidate.Storage.Data = filepath.Join(t.TempDir(), "new-data")
				}
				err = f.r.drainServiceManager(context.Background(), candidate, identity)
				if scenario == "matching legacy peer" {
					if err != nil || calls != 3 {
						t.Fatalf("matching peer: calls=%d error=%v", calls, err)
					}
				} else {
					requireCode(t, err, ErrControl)
				}
				if !reflect.DeepEqual(f.m.Store.View().Runners[runner], before) {
					t.Fatal("execution authority changed")
				}
				if f.mutated() {
					t.Fatal("drain dispatched native mutation")
				}
				if scenario == "changed storage" {
					if _, err := os.Stat(candidate.Storage.State); !os.IsNotExist(err) {
						t.Fatal("candidate storage created")
					}
				}
				if (scenario == "changed storage" || scenario == "foreign peer") && f.m.Store.View().Stopping {
					t.Fatal("foreign peer admitted Stop")
				}
			})
		}
	}
}

func TestServiceStopInactiveFallbackAndRaces(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		for _, scenario := range []string{"inactive", "became active before proof", "became active after proof", "inspection unavailable", "loaded inactive launchd"} {
			t.Run(platform+"/"+scenario, func(t *testing.T) {
				if scenario == "loaded inactive launchd" && platform != "darwin" {
					t.Skip("launchd only")
				}
				f := newReloadFixture(t, platform)
				f.pid, f.loaded = 0, false
				definition, _ := os.ReadFile(f.r.Unit)
				identity, err := f.r.admitServiceManager(context.Background(), definition)
				if err != nil {
					t.Fatal(err)
				}
				f.m.Store.Close()
				f.r.Control = func(context.Context, Config, ControlRequest, int) (ControlResponse, int, error) {
					t.Fatal("inactive service contacted foreground control")
					return ControlResponse{}, 0, nil
				}
				reads := 0
				f.onCommand = func(string) error {
					reads++
					if scenario == "inspection unavailable" {
						return errors.New("private native failure")
					}
					if scenario == "became active before proof" || scenario == "became active after proof" && reads > 1 {
						f.pid, f.loaded = 101, true
					}
					if scenario == "loaded inactive launchd" {
						f.loaded = true
					}
					return nil
				}
				err = f.r.drainServiceManager(context.Background(), f.c, identity)
				if scenario == "inactive" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					requireCode(t, err, ErrControl)
				}
				if f.mutated() {
					t.Fatal("offline proof dispatched native mutation")
				}
			})
		}
	}
}

func TestServiceStopControlChecksProcessStartBeforeSending(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix peer credentials")
	}
	m, c, _, _, _ := testManager(t)
	server, err := m.ServeControl()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, _, err = sendControlIdentity(context.Background(), c, ControlRequest{Action: "stop"}, os.Getpid(), "foreign-start", true)
	if err == nil || m.Store.View().Stopping {
		t.Fatal("changed process start accepted Stop")
	}
	start, err := tartRunProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	response, peer, err := sendControlIdentity(context.Background(), c, ControlRequest{Action: "status"}, os.Getpid(), start, true)
	if err != nil || peer != os.Getpid() || response.Status == nil {
		t.Fatalf("original peer rejected: %v", err)
	}
}

func TestServiceStopOriginalExitRequiresGenerationCompletion(t *testing.T) {
	for _, scenario := range []string{"completed original", "unfinished original", "missing original state", "unconfirmed Stop"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReloadFixture(t, "linux")
			definition, _ := os.ReadFile(f.r.Unit)
			identity, err := f.r.admitServiceManager(context.Background(), definition)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "unfinished original" {
				seedRunner(t, f.m, sortedPools(f.m.Store.View())[0].ID, Busy)
			}
			calls := 0
			f.r.Control = func(ctx context.Context, c Config, req ControlRequest, pid int) (ControlResponse, int, error) {
				calls++
				if calls == 1 {
					return ControlResponse{SchemaVersion: 1, Status: statusOf(f.m.Store.View(), true)}, pid, nil
				}
				if calls == 2 {
					if scenario != "unconfirmed Stop" {
						if err := f.m.Stop(false); err != nil {
							t.Fatal(err)
						}
					}
					f.m.Store.Close()
					f.pid, f.loaded = 0, false
					if scenario == "missing original state" {
						if err := os.Remove(filepath.Join(c.Storage.State, "state.sqlite")); err != nil {
							t.Fatal(err)
						}
					}
					if scenario != "unconfirmed Stop" {
						return ControlResponse{SchemaVersion: 1}, pid, nil
					}
				}
				return ControlResponse{}, 0, errors.New("private exited socket")
			}
			err = f.r.drainServiceManager(context.Background(), f.c, identity)
			if scenario == "completed original" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireCode(t, err, ErrControl)
			}
			if scenario == "missing original state" {
				if _, err := os.Stat(filepath.Join(f.c.Storage.State, "state.sqlite")); !os.IsNotExist(err) {
					t.Fatal("missing original replaced by fresh state")
				}
			}
		})
	}
}

func TestServiceStopAdmissionRejectsPIDReuseDuringArgumentProof(t *testing.T) {
	f := newReloadFixture(t, "linux")
	definition, _ := os.ReadFile(f.r.Unit)
	start := "original"
	f.r.ProcessStart = func(int) (string, error) { return start, nil }
	arguments := f.r.ProcessArgs
	f.r.ProcessArgs = func(pid int) ([]string, error) {
		args, err := arguments(pid)
		start = "recycled"
		return args, err
	}
	_, err := f.r.admitServiceManager(context.Background(), definition)
	requireCode(t, err, ErrControl)
	if len(f.actions) != 0 || f.mutated() {
		t.Fatal("recycled admission acquired control or native authority")
	}
}
