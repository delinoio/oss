package runmoor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestDinDAdmissionKeepsSeparateContainerCPULimits(t *testing.T) {
	for _, daemonCPU := range []int{2, 3} {
		t.Run(strconv.Itoa(daemonCPU), func(t *testing.T) {
			c, _ := fixtureStore(t)
			c.Host.CPU, c.Host.MemoryMiB = 2, 32768
			p := c.Pools[0]
			p.Mode, p.DaemonImage = DinD, p.Image
			p.Resources, p.DaemonResources = Resources{2, 16384}, Resources{daemonCPU, 2048}
			c.Pools[0] = p
			s := Snapshot{Installation: newID()}
			r := Runner{ID: newID(), Resources: p.Cost()}
			r.Name = "runmoor-" + r.ID
			listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "docker.sock"))
			if err != nil {
				t.Fatal(err)
			}
			c.DockerSocket = "unix://" + listener.Addr().String()
			created := map[string]container.HostConfig{}
			var createdMu sync.Mutex
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("API-Version", "1.51")
				w.Header().Set("Content-Type", "application/json")
				path := strings.TrimPrefix(req.URL.Path, "/v1.51")
				switch {
				case path == "/_ping":
					_, _ = w.Write([]byte("OK"))
				case path == "/info":
					_ = json.NewEncoder(w).Encode(map[string]any{"OSType": "linux", "Architecture": runtime.GOARCH, "NCPU": 2, "MemTotal": int64(32768) * 1024 * 1024, "CgroupVersion": "2"})
				case strings.HasPrefix(path, "/images/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": p.Image, "Os": "linux", "Architecture": p.Arch})
				case req.Method == http.MethodGet && (strings.HasPrefix(path, "/volumes/") || strings.HasPrefix(path, "/networks/")):
					w.WriteHeader(http.StatusNotFound)
				case path == "/volumes/create":
					var volume struct {
						Name   string
						Labels map[string]string
					}
					if err := json.NewDecoder(req.Body).Decode(&volume); err != nil {
						t.Error(err)
					}
					_ = json.NewEncoder(w).Encode(volume)
				case path == "/networks/create":
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": "network"})
				case path == "/containers/create":
					var body struct {
						HostConfig container.HostConfig
						Labels     map[string]string
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					role := body.Labels[roleKey]
					createdMu.Lock()
					created[role] = body.HostConfig
					createdMu.Unlock()
					if role == "runner" {
						// Stop after observing the actual create request. The fixture
						// does not implement attachment, bootstrap or live execution.
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": role})
				case path == "/containers/init/wait":
					_ = json.NewEncoder(w).Encode(map[string]int{"StatusCode": 0})
				case path == "/containers/init/start" || path == "/containers/daemon/start" || req.Method == http.MethodDelete && path == "/containers/init":
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected Docker request: %s %s", req.Method, path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			err = (&DockerDriver{}).Prepare(context.Background(), c, p, r, s, "fixture-jit", func(Handle) error { return nil })
			createdMu.Lock()
			defer createdMu.Unlock()
			if daemonCPU > 2 {
				requireCode(t, err, ErrCapacity)
				if len(created) != 0 {
					t.Fatal("invalid daemon CPU limit reached container creation")
				}
				return
			}
			requireCode(t, err, ErrDependency)
			if len(created) != 3 {
				t.Fatalf("expected init, daemon and runner requests: %v", created)
			}
			for role, want := range map[string]Resources{"init": {2, 16384}, "runner": {2, 16384}, "daemon": {2, 2048}} {
				got := created[role]
				if got.NanoCPUs != int64(want.CPU)*1e9 || got.Memory != want.MemoryMiB*1024*1024 || got.MemorySwap != got.Memory || got.LogConfig.Type != "none" {
					t.Fatalf("%s container limits changed: %+v", role, got.Resources)
				}
			}
			if !created["daemon"].Privileged || created["daemon"].CgroupnsMode != container.CgroupnsModePrivate {
				t.Fatal("daemon isolation changed")
			}
		})
	}
}
