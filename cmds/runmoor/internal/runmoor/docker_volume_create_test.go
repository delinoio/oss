package runmoor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestDockerPrepareValidatesReturnedVolumesBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name, role               string
		existing, valid, failure bool
	}{
		{name: "absent to foreign installation", role: "work"},
		{name: "owned to foreign runner", role: "work", existing: true},
		{name: "owned to wrong name", role: "work", existing: true},
		{name: "owned to wrong role", role: "work", existing: true},
		{name: "missing labels", role: "work"},
		{name: "missing role", role: "work"},
		{name: "foreign socket", role: "socket"},
		{name: "foreign externals", role: "externals"},
		{name: "foreign docker", role: "docker"},
		{name: "new owned volumes", valid: true},
		{name: "existing owned volumes", existing: true, valid: true},
		{name: "creation failure", role: "work", failure: true},
		{name: "creation transport failure", role: "work", failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, store := fixtureStore(t)
			p := c.Pools[0]
			p.Mode = DinD
			p.DaemonImage = p.Image
			p.DaemonResources = Resources{CPU: 1, MemoryMiB: 128}
			r := Runner{ID: newID(), Name: "rm-fixture", Resources: p.Resources}
			snap := store.View()
			listener, err := net.Listen("unix", filepath.Join(c.Storage.State, "docker.sock"))
			if err != nil {
				t.Fatal(err)
			}
			c.DockerSocket = "unix://" + listener.Addr().String()
			var mu sync.Mutex
			var creates, published []string
			downstream := 0
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("API-Version", "1.51")
				w.Header().Set("Content-Type", "application/json")
				path := strings.TrimPrefix(req.URL.Path, "/v1.51")
				switch {
				case path == "/_ping":
					_, _ = w.Write([]byte("OK"))
				case path == "/info":
					_ = json.NewEncoder(w).Encode(map[string]any{"OSType": "linux", "Architecture": runtime.GOARCH, "NCPU": c.Host.CPU, "MemTotal": c.Host.MemoryMiB * 1024 * 1024, "CgroupVersion": "2"})
				case strings.HasPrefix(path, "/images/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"Os": "linux", "Architecture": runtime.GOARCH})
				case req.Method == http.MethodGet && strings.HasPrefix(path, "/volumes/"):
					name := strings.TrimPrefix(path, "/volumes/")
					if !tc.existing {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					role := strings.TrimPrefix(name, r.Name+"-")
					_ = json.NewEncoder(w).Encode(map[string]any{"Name": name, "Labels": dockerLabels(snap, r, role)})
				case path == "/volumes/create":
					var input struct {
						Name   string
						Labels map[string]string
					}
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					role := strings.TrimPrefix(input.Name, r.Name+"-")
					if !reflect.DeepEqual(input.Labels, dockerLabels(snap, r, role)) {
						t.Error("creation changed ownership labels")
					}
					creates = append(creates, input.Name)
					if tc.failure {
						if tc.name == "creation transport failure" {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
						w.WriteHeader(500)
						_, _ = w.Write([]byte(`{"message":"fixture-sensitive-upstream"}`))
						return
					}
					labels, name := input.Labels, input.Name
					if role == tc.role {
						switch tc.name {
						case "owned to wrong name":
							name = r.Name + "-replacement"
						case "owned to wrong role":
							labels[roleKey] = "socket"
						case "missing labels":
							labels = nil
						case "missing role":
							delete(labels, roleKey)
						case "owned to foreign runner":
							labels[runnerKey] = "foreign"
						default:
							labels[ownerKey] = "foreign"
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Name": name, "Labels": labels})
				case strings.HasPrefix(path, "/networks"):
					downstream++
					if req.Method == http.MethodGet {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "fixture-network"})
				case path == "/containers/create":
					downstream++
					var input struct{ HostConfig container.HostConfig }
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					for _, mount := range input.HostConfig.Mounts {
						if mount.Source != r.Name+"-work" && mount.Source != r.Name+"-socket" && mount.Source != r.Name+"-externals" {
							t.Error("unexpected init mount", mount.Source)
						}
					}
					// Stop after the normal initialization request; no container is executed.
					w.WriteHeader(500)
				default:
					t.Error("unexpected operation", req.Method, path)
					w.WriteHeader(500)
				}
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			err = (&DockerDriver{}).Prepare(context.Background(), c, p, r, snap, "", func(h Handle) error {
				published = append([]string(nil), h.Volumes...)
				return nil
			})
			code := ErrOwnership
			if tc.valid || tc.failure {
				code = ErrDependency
			}
			requireCode(t, err, code)
			if strings.Contains(err.Error(), "fixture-sensitive-upstream") {
				t.Fatal("raw dependency error exposed")
			}
			mu.Lock()
			defer mu.Unlock()
			roles := []string{"work", "socket", "externals", "docker"}
			var want []string
			for _, role := range roles {
				if role == tc.role {
					break
				}
				want = append(want, r.Name+"-"+role)
			}
			if !reflect.DeepEqual(published, want) {
				t.Fatalf("published unverified volumes: got %v want %v", published, want)
			}
			if tc.valid {
				if downstream != 3 || len(creates) != 4 {
					t.Fatalf("owned volume preparation did not reach init: %v %d", creates, downstream)
				}
			} else if downstream != 0 {
				t.Fatalf("unverified volume reached network/container requests: %d", downstream)
			}
		})
	}
}
