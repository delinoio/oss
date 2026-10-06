package runmoor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDockerCleanupRevalidatesVolumeOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		code       ErrorCode
		remove     bool
	}{
		{"owned work", "work", "", true},
		{"owned socket", "socket", "", true},
		{"owned externals", "externals", "", true},
		{"owned docker", "docker", "", true},
		{"foreign installation", "work", ErrOwnership, false},
		{"foreign runner", "work", ErrOwnership, false},
		{"missing labels", "work", ErrOwnership, false},
		{"missing role", "work", ErrOwnership, false},
		{"wrong role", "work", ErrOwnership, false},
		{"unknown role", "work", ErrOwnership, false},
		{"unexpected name", "work", ErrOwnership, false},
		{"mismatched inspection name", "work", ErrOwnership, false},
		{"confirmed absence", "work", "", false},
		{"unavailable inspection", "work", ErrDependency, false},
		{"malformed inspection", "work", ErrDependency, false},
		{"disappeared before removal", "work", "", true},
		{"in use", "work", ErrDependency, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, _, _, pool := testManager(t)
			id := seedRunner(t, m, pool, Cleaning)
			r := m.Store.View().Runners[id]
			name := r.Name + "-" + tc.role
			if tc.name == "unexpected name" {
				name = r.Name + "-foreign"
			}
			listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "docker.sock"))
			if err != nil {
				t.Fatal(err)
			}
			c.DockerSocket = "unix://" + listener.Addr().String()
			if err = m.Store.Update(func(s *Snapshot) error {
				s.Generations[r.Generation] = c
				rr := s.Runners[id]
				rr.Terminated, rr.RemoteRemoved, rr.DiagnosticsSaved = true, true, true
				rr.Handle.Volumes = []string{name}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			snap := m.Store.View()
			var mu sync.Mutex
			var operations []string
			present, recovered := true, false
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("API-Version", "1.51")
				w.Header().Set("Content-Type", "application/json")
				path := strings.TrimPrefix(req.URL.Path, "/v1.51")
				switch {
				case path == "/_ping":
					_, _ = w.Write([]byte("OK"))
				case req.Method == http.MethodGet && (path == "/containers/json" || path == "/networks"):
					_, _ = w.Write([]byte("[]"))
				case req.Method == http.MethodGet && path == "/volumes":
					operations = append(operations, "list")
					// Discovery sees the original owned volume. Only the later
					// inspection observes its replacement, absence or failure.
					_ = json.NewEncoder(w).Encode(map[string]any{"Volumes": []any{map[string]any{"Name": name, "Labels": dockerLabels(snap, *r, tc.role)}}})
				case req.Method == http.MethodGet && path == "/volumes/"+name:
					operations = append(operations, "inspect")
					labels, inspectedName := dockerLabels(snap, *r, tc.role), name
					if !recovered {
						switch tc.name {
						case "foreign installation":
							labels[ownerKey] = "foreign-installation"
						case "foreign runner":
							labels[runnerKey] = "foreign-runner"
						case "missing labels":
							labels = nil
						case "missing role":
							delete(labels, roleKey)
						case "wrong role":
							labels[roleKey] = "socket"
						case "unknown role":
							labels[roleKey] = "foreign"
						case "mismatched inspection name":
							inspectedName = r.Name + "-socket"
						case "confirmed absence":
							present = false
							w.WriteHeader(http.StatusNotFound)
							return
						case "unavailable inspection":
							w.WriteHeader(http.StatusInternalServerError)
							_, _ = w.Write([]byte(`{"message":"fixture-sensitive-body"}`))
							return
						case "malformed inspection":
							_, _ = w.Write([]byte(`{"Name":`))
							return
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Name": inspectedName, "Labels": labels})
				case req.Method == http.MethodDelete && path == "/volumes/"+name:
					operations = append(operations, "delete")
					if force := req.URL.Query().Get("force"); force != "" && force != "false" && force != "0" {
						t.Error("volume cleanup requested force removal")
					}
					if tc.name == "in use" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					present = false
					if tc.name == "disappeared before removal" {
						w.WriteHeader(http.StatusNotFound)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					t.Error("unexpected Docker operation", req.Method, path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			m.Drivers = func(Backend) (Driver, error) { return &DockerDriver{}, nil }
			m.cleanup(context.Background(), id)
			persisted, err := ReadSnapshot(c)
			if err != nil {
				t.Fatal(err)
			}
			rr := persisted.Runners[id]
			if !rr.Terminated || !rr.RemoteRemoved || !reflect.DeepEqual(rr.Handle.Volumes, []string{name}) {
				t.Fatal("cleanup lost its durable execution authority", rr)
			}
			if tc.code == "" {
				if rr.Phase != Completed || !rr.LocalCleaned || rr.Problem != nil {
					t.Fatal("owned removal or confirmed absence did not complete cleanup", rr)
				}
			} else {
				if rr.Problem == nil || rr.Problem.Code != tc.code {
					t.Fatalf("expected %s, got %v", tc.code, rr.Problem)
				}
				want := Cleaning
				if tc.code == ErrOwnership {
					want = Quarantined
				}
				if rr.LocalCleaned || rr.Phase != want || !rr.CompletedAt.IsZero() {
					t.Fatal("failed cleanup discarded its unfinished record", rr)
				}
				if strings.Contains(rr.Problem.Error(), "fixture-sensitive-body") {
					t.Fatal("cleanup exposed an upstream response body")
				}
			}
			mu.Lock()
			got, remains := append([]string(nil), operations...), present
			mu.Unlock()
			want := []string{"list", "inspect"}
			if tc.remove {
				want = append(want, "delete")
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("volume removal did not follow fresh inspection", got)
			}
			if remains != (tc.code != "") {
				t.Fatal("cleanup removed an unverified or in-use volume")
			}
			if tc.name == "unavailable inspection" {
				mu.Lock()
				recovered = true
				mu.Unlock()
				m.cleanup(context.Background(), id)
				rr = m.Store.View().Runners[id]
				if rr.Phase != Completed || !rr.LocalCleaned || rr.Problem != nil {
					t.Fatal("cleanup did not retry after inspection recovered", rr)
				}
				mu.Lock()
				got, remains = append([]string(nil), operations...), present
				mu.Unlock()
				if remains || !reflect.DeepEqual(got, []string{"list", "inspect", "list", "inspect", "delete"}) {
					t.Fatal("cleanup retry bypassed fresh ownership inspection", got)
				}
			}
		})
	}
}

func TestDockerCleanupRetryVerifiesRecordedContainers(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		code       ErrorCode
	}{
		{"copied labels runner replacement", "runner", ErrOwnership},
		{"copied labels daemon replacement", "daemon", ErrOwnership},
		{"copied labels renamed runner replacement", "runner", ErrOwnership},
		{"foreign init", "init", ErrOwnership},
		{"wrong role", "runner", ErrOwnership},
		{"foreign renamed runner", "runner", ErrOwnership},
		{"unavailable runner inspection", "runner", ErrDependency},
		{"unavailable daemon inspection", "daemon", ErrDependency},
		{"missing container state", "runner", ErrCleanup},
		{"running container", "runner", ErrCleanup},
		{"restarting container", "daemon", ErrCleanup},
		{"stopped originals", "", ""},
		{"renamed original", "runner", ""},
		{"confirmed absence", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, remote, _, pool := testManager(t)
			id := seedRunner(t, m, pool, Cleaning)
			r := m.Store.View().Runners[id]
			listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "docker.sock"))
			if err != nil {
				t.Fatal(err)
			}
			c.DockerSocket = "unix://" + listener.Addr().String()
			if err = m.Store.Update(func(s *Snapshot) error {
				s.Generations[r.Generation] = c
				r := s.Runners[id]
				r.Handle = Handle{Container: "runner-id", Daemon: "daemon-id"}
				r.Terminated, r.RemoteRemoved, r.DiagnosticsSaved = true, true, true
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			snap := m.Store.View()
			r = snap.Runners[id]
			type fixtureContainer struct {
				name, role string
				labels     map[string]string
			}
			containers := map[string]fixtureContainer{}
			for _, role := range []string{"runner", "daemon", "init"} {
				name := r.Name
				if role != "runner" {
					name += "-" + role
				}
				cid := role + "-id"
				labels := dockerLabels(snap, *r, role)
				if tc.role == role {
					switch tc.name {
					case "copied labels runner replacement", "copied labels daemon replacement", "copied labels renamed runner replacement":
						cid = "replacement-id"
					case "foreign init", "foreign renamed runner":
						labels[ownerKey] = "another-installation"
					case "wrong role":
						labels[roleKey] = "daemon"
					}
					if tc.name == "foreign renamed runner" || tc.name == "renamed original" || tc.name == "copied labels renamed runner replacement" {
						name = "renamed-container"
					}
				}
				containers[cid] = fixtureContainer{name, role, labels}
			}
			if tc.name == "confirmed absence" {
				clear(containers)
			}
			var mutations, inspections atomic.Int32
			var recovered atomic.Bool
			var containerMu sync.Mutex
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				containerMu.Lock()
				defer containerMu.Unlock()
				w.Header().Set("API-Version", "1.51")
				w.Header().Set("Content-Type", "application/json")
				path := strings.TrimPrefix(req.URL.Path, "/v1.51")
				if path == "/_ping" {
					_, _ = w.Write([]byte("OK"))
					return
				}
				if req.Method != http.MethodGet {
					mutations.Add(1)
					cid := strings.TrimPrefix(path, "/containers/")
					if req.Method == http.MethodDelete && path == "/containers/"+cid {
						if _, ok := containers[cid]; ok {
							delete(containers, cid)
							w.WriteHeader(http.StatusNoContent)
							return
						}
					}
					t.Errorf("unexpected Docker mutation: %s %s", req.Method, path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				switch path {
				case "/containers/json":
					items := []any{}
					for cid, v := range containers {
						if ownedDocker(v.labels, snap, *r) {
							items = append(items, map[string]any{"Id": cid, "Names": []string{"/" + v.name}, "Labels": v.labels, "State": "exited"})
						}
					}
					_ = json.NewEncoder(w).Encode(items)
					return
				case "/networks":
					_, _ = w.Write([]byte("[]"))
					return
				case "/volumes":
					_, _ = w.Write([]byte(`{"Volumes":[]}`))
					return
				}
				if strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json") {
					inspections.Add(1)
					ref := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
					for cid, v := range containers {
						if ref != cid && ref != v.name {
							continue
						}
						state := map[string]any{"Running": false, "Restarting": false}
						if v.role == tc.role {
							switch tc.name {
							case "unavailable runner inspection", "unavailable daemon inspection":
								if !recovered.Load() {
									w.WriteHeader(http.StatusInternalServerError)
									return
								}
							case "missing container state":
								state = nil
							case "running container":
								state["Running"] = true
							case "restarting container":
								state["Restarting"] = true
							}
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"Id": cid, "Config": map[string]any{"Labels": v.labels}, "State": state})
						return
					}
					w.WriteHeader(http.StatusNotFound)
					return
				}
				t.Errorf("unexpected Docker read: %s", path)
				w.WriteHeader(http.StatusInternalServerError)
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			m.Drivers = func(Backend) (Driver, error) { return &DockerDriver{}, nil }
			attempts := 2
			if tc.code == ErrDependency {
				attempts = 3
			}
			for attempt := range attempts {
				code := tc.code
				if attempt == 2 {
					// Restored inspection permits cleanup without repeating Stop.
					recovered.Store(true)
					code = ""
				}
				beforeInspections := inspections.Load()
				m.cleanup(context.Background(), id)
				s := m.Store.View()
				got := s.Runners[id]
				if !got.Terminated || !got.RemoteRemoved || !got.DiagnosticsSaved || !reflect.DeepEqual(got.Handle, r.Handle) {
					t.Fatal("cleanup changed confirmed termination or recorded ownership", got)
				}
				if resources, count, vms := usage(s); resources != (Resources{}) || count != 0 || vms != 0 {
					t.Fatal("cleanup restored a confirmed terminated reservation", resources, count, vms)
				}
				if remote.removed != 0 {
					t.Fatal("cleanup repeated confirmed remote removal")
				}
				if code != "" {
					if mutations.Load() != 0 || got.LocalCleaned || got.Phase == Completed {
						t.Fatalf("attempt %d mutated unverified resources or completed cleanup: mutations=%d runner=%+v", attempt, mutations.Load(), got)
					}
					requireCode(t, got.Problem, code)
					want := Cleaning
					if code == ErrOwnership {
						want = Quarantined
					}
					if got.Phase != want {
						t.Fatalf("cleanup phase = %s, want %s", got.Phase, want)
					}
				} else {
					if got.Phase != Completed || !got.LocalCleaned || got.Problem != nil {
						t.Fatal("verified cleanup did not complete", got)
					}
					want := int32(3)
					if tc.name == "confirmed absence" {
						want = 0
					}
					if mutations.Load() != want {
						t.Fatalf("cleanup mutations = %d, want %d", mutations.Load(), want)
					}
				}
				if inspections.Load() == beforeInspections {
					t.Fatal("cleanup did not inspect deterministic names or recorded IDs")
				}
			}
		})
	}
}
