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
