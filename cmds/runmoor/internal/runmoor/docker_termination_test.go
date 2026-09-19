package runmoor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDockerForceStopVerifiesRecordedContainers(t *testing.T) {
	for _, tc := range []struct {
		name, role, ref string
		code            ErrorCode
	}{
		{"foreign runner", "runner", "name", ErrOwnership},
		{"foreign daemon", "daemon", "name", ErrOwnership},
		{"foreign init", "init", "name", ErrOwnership},
		{"replaced runner", "runner", "name", ErrOwnership},
		{"replaced daemon", "daemon", "name", ErrOwnership},
		{"wrong role", "runner", "name", ErrOwnership},
		{"foreign renamed runner", "runner", "handle", ErrOwnership},
		{"foreign renamed daemon", "daemon", "handle", ErrOwnership},
		{"unavailable", "runner", "name", ErrDependency},
		{"omitted live runner", "runner", "handle", ErrCleanup},
		{"replaced during stop", "runner", "name", ErrOwnership},
		{"confirmed absence", "runner", "name", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, remote, _, pool := testManager(t)
			id := seedRunner(t, m, pool, Busy)
			r := m.Store.View().Runners[id]
			listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "docker.sock"))
			if err != nil {
				t.Fatal(err)
			}
			c.DockerSocket = "unix://" + listener.Addr().String()
			if err = m.Store.Update(func(s *Snapshot) error {
				s.Generations[r.Generation] = c
				s.Runners[id].Handle = Handle{Container: "runner-id", Daemon: "daemon-id"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			snap := m.Store.View()
			target := r.Name
			if tc.role != "runner" {
				target += "-" + tc.role
			}
			if tc.ref == "handle" {
				target = tc.role + "-id"
			}
			var mutations atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("API-Version", "1.51")
				w.Header().Set("Content-Type", "application/json")
				path := strings.TrimPrefix(req.URL.Path, "/v1.51")
				if path == "/_ping" {
					_, _ = w.Write([]byte("OK"))
					return
				}
				if req.Method != http.MethodGet {
					mutations.Add(1)
					if tc.name == "replaced during stop" && path == "/containers/runner-id/stop" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					t.Error("termination mutated an unverified resource", path)
					w.WriteHeader(500)
					return
				}
				switch path {
				case "/containers/json", "/networks":
					if path == "/containers/json" && tc.name == "replaced during stop" {
						_ = json.NewEncoder(w).Encode([]any{map[string]any{"Id": "runner-id", "Labels": dockerLabels(snap, *r, "runner")}})
					} else {
						_, _ = w.Write([]byte("[]"))
					}
					return
				case "/volumes":
					_, _ = w.Write([]byte(`{"Volumes":[]}`))
					return
				}
				if path != "/containers/"+target+"/json" || tc.name == "confirmed absence" {
					w.WriteHeader(404)
					return
				}
				if tc.name == "unavailable" {
					w.WriteHeader(500)
					return
				}
				labels := dockerLabels(snap, *r, tc.role)
				cid := tc.role + "-id"
				if strings.HasPrefix(tc.name, "foreign") || (tc.name == "replaced during stop" && mutations.Load() > 0) {
					labels[ownerKey] = "another-installation"
				}
				if strings.HasPrefix(tc.name, "replaced") && tc.name != "replaced during stop" {
					cid = "replacement-id"
				}
				if tc.name == "wrong role" {
					labels[roleKey] = "daemon"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": cid, "Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": true}})
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			m.Drivers = func(Backend) (Driver, error) { return &DockerDriver{}, nil }
			if err = m.Stop(true); err != nil {
				t.Fatal(err)
			}
			m.cleanup(context.Background(), id)
			r = m.Store.View().Runners[id]
			if tc.code == "" {
				if r.Phase != Completed || !r.Terminated || !r.LocalCleaned {
					t.Fatal("confirmed absence did not allow cleanup", r)
				}
				return
			}
			requireCode(t, r.Problem, tc.code)
			if r.Terminated || r.LocalCleaned || r.Phase == Completed || remote.removed != 0 {
				t.Fatal("unconfirmed termination released execution ownership", r)
			}
			if tc.code == ErrOwnership && r.Phase != Quarantined {
				t.Fatal("ambiguous ownership was not quarantined")
			}
			if _, count, _ := usage(m.Store.View()); count != 1 {
				t.Fatal("ambiguous execution lost its reservation")
			}
			if tc.name != "replaced during stop" && mutations.Load() != 0 {
				t.Fatal("unverified resources were mutated")
			}
		})
	}
}
