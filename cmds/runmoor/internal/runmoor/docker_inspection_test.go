package runmoor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerInspectionRequiresOwnedLiveDindDaemon(t *testing.T) {
	for _, state := range []string{"running", "stopped", "missing", "foreign", "replaced", "unavailable", "unpublished"} {
		t.Run(state, func(t *testing.T) {
			m, c, remote, _, pool := testManager(t)
			id := seedRunner(t, m, pool, Idle)
			r := m.Store.View().Runners[id]
			listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "docker.sock"))
			if err != nil {
				t.Fatal(err)
			}
			c.DockerSocket = "unix://" + listener.Addr().String()
			if err = m.Store.Update(func(s *Snapshot) error {
				s.Generations[r.Generation] = c
				s.Pools[pool].Spec.Mode = DinD
				s.Runners[id].Handle = Handle{Container: "runner", Daemon: "daemon"}
				if state == "unpublished" {
					s.Runners[id].Handle = Handle{}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			snap := m.Store.View()
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("API-Version", "1.51")
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Path == "/_ping" {
					_, _ = w.Write([]byte("OK"))
					return
				}
				if req.Method != http.MethodGet {
					t.Error("inspection mutated Docker")
					w.WriteHeader(500)
					return
				}
				role, cid, running := "runner", "runner", true
				if strings.HasSuffix(req.URL.Path, r.Name+"-daemon/json") {
					role, cid = "daemon", "daemon"
					switch state {
					case "missing":
						w.WriteHeader(404)
						return
					case "unavailable":
						w.WriteHeader(500)
						return
					case "stopped":
						running = false
					case "replaced":
						cid = "replacement"
					}
				} else if !strings.HasSuffix(req.URL.Path, r.Name+"/json") {
					t.Error("unexpected Docker request", req.URL.Path)
					w.WriteHeader(404)
					return
				}
				labels := dockerLabels(snap, *r, role)
				if role == "daemon" && state == "foreign" {
					labels[runnerKey] = newID()
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Id": cid, "Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": running, "ExitCode": 0}})
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			m.Drivers = func(Backend) (Driver, error) { return &DockerDriver{}, nil }
			m.inspect(context.Background(), id)
			r = m.Store.View().Runners[id]
			want := Idle
			if state == "missing" || state == "stopped" {
				want = Cleaning
			}
			if state == "foreign" || state == "replaced" {
				want = Quarantined
			}
			if r.Phase != want || r.Terminated {
				t.Fatal("daemon inspection lost lifecycle/termination safety", r.Phase)
			}
			if state == "unavailable" {
				requireCode(t, r.Problem, ErrDependency)
			}
			if state == "unpublished" && r.Handle.Daemon != "daemon" {
				t.Fatal("crash recovery lost the owned daemon")
			}
			if remote.removed != 0 {
				t.Fatal("inspection bypassed busy-aware cleanup")
			}
		})
	}
}
