package runmoor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/golang-jwt/jwt/v4"
)

// Exercise the official multi-request client, including token refresh before
// DELETE. These TLS fixtures never contact GitHub or an execution backend.
func absenceFixture(t *testing.T, stage string, refresh bool, resource http.HandlerFunc) (*GitHub, *atomic.Int64) {
	t.Helper()
	credential := "fixture-pat"
	auth := PAT
	if stage == "installation" {
		auth = App
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		credential = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	}
	t.Setenv("RUNMOOR_ABSENCE_CREDENTIAL", credential)
	expiry := time.Now().Add(time.Hour)
	if refresh {
		expiry = time.Now().Add(30 * time.Second)
	}
	admin, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": expiry.Unix()}).SignedString([]byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	requests := &atomic.Int64{}
	client := mockGitHubHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		currentStage := ""
		switch {
		case r.URL.Path == "/app/installations/42/access_tokens":
			currentStage = "installation"
		case strings.HasSuffix(r.URL.Path, "/actions/runners/registration-token"):
			currentStage = "registration"
		case r.URL.Path == "/actions/runner-registration":
			currentStage = "admin"
		}
		if stage != "" && currentStage == stage && (!refresh || requests.Load() > 0) {
			w.WriteHeader(404)
			// An upstream typed exception must not bypass the request-origin proof.
			json.NewEncoder(w).Encode(map[string]any{"typeName": "AgentNotFoundException", "message": "fixture-private-token"})
			return
		}
		switch currentStage {
		case "installation":
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"token": "fixture-installation", "expires_at": time.Now().Add(time.Hour)})
		case "registration":
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"token": "fixture-registration"})
		case "admin":
			json.NewEncoder(w).Encode(map[string]any{"url": "https://api.github.com/deployment", "token": admin})
		default:
			requests.Add(1)
			resource(w, r)
		}
	}))
	g, err := newGitHub(Connection{Target: "https://github.com/example/repo", Auth: auth, ClientID: "fixture-app", InstallationID: 42, Credential: SecretRef{Env: "RUNMOOR_ABSENCE_CREDENTIAL"}}, client)
	if err != nil {
		t.Fatal(err)
	}
	return g, requests
}

func ownedFixtureResource(p PoolState, r Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodDelete:
			w.WriteHeader(204)
		case strings.Contains(req.URL.Path, "runnerscalesets"):
			json.NewEncoder(w).Encode(scaleset.RunnerScaleSet{ID: p.ScaleSetID, Name: p.Spec.ScaleSet, Labels: []scaleset.Label{{Name: p.OwnerLabel}}})
		default:
			json.NewEncoder(w).Encode(scaleset.RunnerReference{ID: r.GitHubID, Name: r.Name, RunnerScaleSetID: p.ScaleSetID})
		}
	}
}

func TestAuthentication404RetainsRemoteAuthority(t *testing.T) {
	p := PoolState{ScaleSetID: 17, OwnerLabel: "owned", Spec: Pool{ScaleSet: "fixture"}}
	r := Runner{GitHubID: 23, Name: "fixture-runner"}
	for _, stage := range []string{"installation", "registration", "admin"} {
		for _, operation := range []string{"find", "remove", "pool"} {
			for _, refresh := range []bool{false, true} {
				if refresh && operation == "find" {
					continue
				}
				t.Run(stage+"/"+operation+map[bool]string{false: "/initial", true: "/refresh"}[refresh], func(t *testing.T) {
					g, requests := absenceFixture(t, stage, refresh, ownedFixtureResource(p, r))
					var err error
					switch operation {
					case "find":
						var found bool
						found, err = g.Find(context.Background(), p, r)
						if found {
							t.Fatal("authentication failure claimed presence")
						}
					case "remove":
						err = g.Remove(context.Background(), p, r)
					case "pool":
						err = g.DeletePool(context.Background(), p)
					}
					if err == nil {
						t.Fatal("authentication 404 admitted resource absence")
					}
					if strings.Contains(err.Error(), "fixture-private-token") {
						t.Fatal("upstream body leaked")
					}
					want := int64(0)
					if refresh {
						want = 1
					}
					if requests.Load() != want {
						t.Fatalf("resource requests=%d want=%d", requests.Load(), want)
					}
				})
			}
		}
	}
}

func TestAuthentication404PreventsLocalCleanupAndPoolRetirement(t *testing.T) {
	for _, stage := range []string{"installation", "registration", "admin"} {
		t.Run(stage, func(t *testing.T) {
			m, _, _, driver, pool := testManager(t)
			id := seedRunner(t, m, pool, Cleaning)
			before := m.Store.View()
			p, r := *before.Pools[pool], *before.Runners[id]
			driver.live[id] = true
			g, requests := absenceFixture(t, stage, false, ownedFixtureResource(p, r))
			m.RemoteFactory = func(Connection) (Remote, error) { return g, nil }
			m.cleanup(context.Background(), id)
			after := m.Store.View().Runners[id]
			if driver.stops != 0 || !driver.live[id] || after.RemoteRemoved || after.Terminated || after.Phase != Cleaning || after.Resources != r.Resources || after.Generation != r.Generation {
				t.Fatalf("authentication failure released local ownership: runner=%+v stops=%d", after, driver.stops)
			}
			if err := m.Store.Update(func(s *Snapshot) error {
				s.Pools[pool].Phase = Draining
				if s.RetirementAuthority == nil {
					s.RetirementAuthority = map[string]Connection{}
				}
				if s.RetirementValidation == nil {
					s.RetirementValidation = map[string]string{}
				}
				s.RetirementAuthority[pool] = p.Connection
				s.RetirementValidation[pool] = "fixture-validation"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			m.retirePool(context.Background(), pool)
			snapshot := m.Store.View()
			if snapshot.Pools[pool].Phase != Draining || snapshot.Pools[pool].ScaleSetID != p.ScaleSetID || snapshot.RetirementValidation[pool] != "fixture-validation" {
				t.Fatal("authentication failure retired remote authority")
			}
			if requests.Load() != 0 {
				t.Fatal("resource request occurred after failed authentication")
			}
		})
	}
}

func TestOwnedResourceAbsenceAndProtections(t *testing.T) {
	p := PoolState{ScaleSetID: 17, OwnerLabel: "owned", Spec: Pool{ScaleSet: "fixture"}}
	r := Runner{GitHubID: 23, Name: "fixture-runner"}
	for _, operation := range []string{"find", "remove", "pool"} {
		for _, phase := range []string{"lookup", "delete"} {
			if operation == "find" && phase == "delete" {
				continue
			}
			t.Run(operation+"/"+phase, func(t *testing.T) {
				resource := ownedFixtureResource(p, r)
				g, requests := absenceFixture(t, "", false, func(w http.ResponseWriter, req *http.Request) {
					if phase == "lookup" || req.Method == http.MethodDelete {
						w.WriteHeader(404)
						json.NewEncoder(w).Encode(map[string]any{"typeName": "AgentNotFoundException", "message": "not found"})
						return
					}
					resource(w, req)
				})
				var err error
				switch operation {
				case "find":
					var found bool
					found, err = g.Find(context.Background(), p, r)
					if found {
						t.Fatal("absent runner reported present")
					}
				case "remove":
					err = g.Remove(context.Background(), p, r)
				case "pool":
					err = g.DeletePool(context.Background(), p)
				}
				if err != nil {
					t.Fatal(err)
				}
				want := int64(1)
				if phase == "delete" {
					want = 2
				}
				if requests.Load() != want {
					t.Fatalf("resource requests=%d want=%d", requests.Load(), want)
				}
			})
		}
	}
	for _, scenario := range []string{"busy", "transient", "typed-non404", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			resource := ownedFixtureResource(p, r)
			g, _ := absenceFixture(t, "", false, func(w http.ResponseWriter, req *http.Request) {
				if scenario == "transient" || scenario == "typed-non404" {
					w.WriteHeader(503)
					if scenario == "typed-non404" {
						json.NewEncoder(w).Encode(map[string]any{"typeName": "AgentNotFoundException"})
					}
					return
				}
				if scenario == "foreign" {
					json.NewEncoder(w).Encode(scaleset.RunnerReference{ID: r.GitHubID, Name: "foreign", RunnerScaleSetID: p.ScaleSetID})
					return
				}
				if req.Method == http.MethodDelete {
					w.WriteHeader(409)
					json.NewEncoder(w).Encode(map[string]any{"typeName": "JobStillRunningException"})
					return
				}
				resource(w, req)
			})
			err := g.Remove(context.Background(), p, r)
			want := ErrBusy
			if scenario == "transient" || scenario == "typed-non404" {
				want = ErrRetry
			}
			if scenario == "foreign" {
				want = ErrOwnership
			}
			requireCode(t, err, want)
		})
	}
}

func TestNamedRunner404RequiresAuthenticatedResourceRequest(t *testing.T) {
	for _, stage := range []string{"registration", ""} {
		t.Run(stage, func(t *testing.T) {
			g, requests := absenceFixture(t, stage, false, func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/_apis/distributedtask/pools/0/agents") || req.URL.Query().Get("agentName") != "fixture-name" {
					t.Error("wrong named resource request")
				}
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]any{"typeName": "AgentNotFoundException"})
			})
			found, err := g.Find(context.Background(), PoolState{ScaleSetID: 17}, Runner{Name: "fixture-name"})
			if found || (err == nil) != (stage == "") {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if (requests.Load() == 0) != (stage != "") {
				t.Fatal("wrong resource boundary")
			}
		})
	}
}
