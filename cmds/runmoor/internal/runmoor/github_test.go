package runmoor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/actions/scaleset"
	"github.com/golang-jwt/jwt/v4"
	"github.com/hashicorp/go-retryablehttp"
)

func mockGitHubHTTP(t *testing.T, h http.Handler) *retryablehttp.Client {
	t.Helper()
	server := httptest.NewTLSServer(h)
	t.Cleanup(server.Close)
	c := newHTTPClient()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = server.Certificate().DNSNames[0]
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	c.HTTPClient.Transport = transport
	return c
}
func TestOfficialClientRepositoryOrganizationAppPAT(t *testing.T) {
	for _, scope := range []string{"example/repo", "example"} {
		for _, auth := range []AuthKind{PAT, App} {
			t.Run(scope+"/"+string(auth), func(t *testing.T) {
				credential := "fixture-pat-only"
				if auth == App {
					key, e := rsa.GenerateKey(rand.Reader, 2048)
					if e != nil {
						t.Fatal(e)
					}
					credential = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
				}
				t.Setenv("RUNMOOR_TEST_CREDENTIAL", credential)
				adminToken, e := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("fixture-admin-key"))
				if e != nil {
					t.Fatal(e)
				}
				var mu sync.Mutex
				var created *scaleset.RunnerScaleSet
				registration, appExchange := false, false
				httpClient := mockGitHubHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.URL.Path == "/app/installations/42/access_tokens":
						appExchange = true
						w.WriteHeader(201)
						json.NewEncoder(w).Encode(map[string]any{"token": "fixture-installation", "expires_at": time.Now().Add(time.Hour)})
					case strings.HasSuffix(r.URL.Path, "/actions/runners/registration-token"):
						registration = true
						want := "/repos/" + scope + "/actions/runners/registration-token"
						if !strings.Contains(scope, "/") {
							want = "/orgs/" + scope + "/actions/runners/registration-token"
						}
						if r.URL.Path != want {
							t.Errorf("wrong registration scope: %s", r.URL.Path)
						}
						w.WriteHeader(201)
						json.NewEncoder(w).Encode(map[string]any{"token": "fixture-registration", "expires_at": time.Now().Add(time.Hour)})
					case r.URL.Path == "/actions/runner-registration":
						json.NewEncoder(w).Encode(map[string]any{"url": "https://api.github.com", "token": adminToken})
					case strings.Contains(r.URL.Path, "runnergroups"):
						json.NewEncoder(w).Encode(map[string]any{"count": 1, "value": []scaleset.RunnerGroup{{ID: 1, Name: "default", IsDefault: true}}})
					case strings.HasSuffix(r.URL.Path, "runnerscalesets") && r.Method == "GET":
						values := []*scaleset.RunnerScaleSet{}
						if created != nil {
							values = append(values, created)
						}
						json.NewEncoder(w).Encode(map[string]any{"count": len(values), "value": values})
					case strings.HasSuffix(r.URL.Path, "runnerscalesets") && r.Method == "POST":
						var v scaleset.RunnerScaleSet
						if json.NewDecoder(r.Body).Decode(&v) != nil {
							t.Error("bad scale-set request")
						}
						v.ID = 17
						created = &v
						w.WriteHeader(200)
						json.NewEncoder(w).Encode(v)
					default:
						t.Errorf("unexpected mock API request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(404)
					}
				}))
				conn := Connection{Name: "test", Target: "https://github.com/" + scope, Auth: auth, Credential: SecretRef{Env: "RUNMOOR_TEST_CREDENTIAL"}}
				if auth == App {
					conn.ClientID = "123"
					conn.InstallationID = 42
				}
				g, e := newGitHub(conn, httpClient)
				if e != nil {
					t.Fatal(e)
				}
				p := PoolState{Spec: Pool{ScaleSet: "fixture", Labels: []string{"linux"}}, OwnerLabel: "runmoor-owner-" + newID()}
				mark := false
				id, e := g.Ensure(context.Background(), p, func() error { mark = true; return nil })
				if e != nil {
					t.Fatal(e)
				}
				if id != 17 || !mark || !registration || (auth == App && !appExchange) {
					t.Fatal("authentication or creation path not exercised")
				}
				p.ScaleSetID = id
				if _, e = g.Ensure(context.Background(), p, func() error { t.Fatal("created existing scale set"); return nil }); e != nil {
					t.Fatal(e)
				}
				p.OwnerLabel = "runmoor-owner-foreign"
				_, e = g.Ensure(context.Background(), p, func() error { return nil })
				requireCode(t, e, ErrOwnership)
			})
		}
	}
}
func TestSDKFailureClassificationNeverLeaksResponse(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Setenv("RUNMOOR_TEST_CREDENTIAL", "fixture-management-secret")
			httpClient := mockGitHubHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status == 429 {
					w.Header().Set("Retry-After", "120")
				}
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				w.Write([]byte("fixture-management-secret raw-job-output signed-url?secret=hidden"))
			}))
			g, e := newGitHub(Connection{Target: "https://github.com/example/repo", Auth: PAT, Credential: SecretRef{Env: "RUNMOOR_TEST_CREDENTIAL"}}, httpClient)
			if e != nil {
				t.Fatal(e)
			}
			e = g.Check(context.Background(), Pool{})
			want := ErrRetry
			if status == 401 || status == 403 {
				want = ErrAuth
			}
			requireCode(t, e, want)
			if strings.Contains(e.Error(), "fixture-management-secret") || strings.Contains(e.Error(), "raw-job-output") {
				t.Fatal("provider response leaked")
			}
			if status == 429 && retryDelay(0, e) < 119*time.Second {
				t.Fatal("retry header lost during SDK classification")
			}
		})
	}
}
func TestAssignmentRaceClassifiedWithoutDeletion(t *testing.T) {
	e := remoteCall(context.Background(), func(context.Context) error { return scaleset.JobStillRunningError })
	requireCode(t, e, ErrBusy)
}

func TestOfficialSessionRefreshesExpiredTokensAndHandlesRevocation(t *testing.T) {
	t.Setenv("RUNMOOR_TEST_CREDENTIAL", "fixture-pat")
	admin, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := newID()
	var mu sync.Mutex
	refreshed, polls, acks := 0, 0, 0
	revoked := false
	client := mockGitHubHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/actions/runners/registration-token"):
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"token": "registration"})
		case r.URL.Path == "/actions/runner-registration":
			json.NewEncoder(w).Encode(map[string]any{"url": "https://api.github.com", "token": admin})
		case strings.Contains(r.URL.Path, "/sessions") && (r.Method == "POST" || r.Method == "PATCH"):
			if r.Method == "PATCH" {
				if revoked {
					w.WriteHeader(403)
					return
				}
				refreshed++
			}
			json.NewEncoder(w).Encode(map[string]any{"sessionId": sessionID, "messageQueueUrl": "https://api.github.com/fixture-queue", "messageQueueAccessToken": "queue-secret", "statistics": map[string]any{"totalAssignedJobs": 2}})
		case r.URL.Path == "/fixture-queue" && r.Method == "GET":
			polls++
			if polls == 1 || revoked {
				w.WriteHeader(401)
				return
			}
			if r.Header.Get(scaleset.HeaderScaleSetMaxCapacity) != "3" {
				t.Error("capacity missing from poll")
			}
			json.NewEncoder(w).Encode(map[string]any{"messageId": 7, "messageType": "RunnerScaleSetJobMessages", "statistics": map[string]any{"totalAssignedJobs": 2}, "body": "[]"})
		case r.URL.Path == "/fixture-queue/7" && r.Method == "DELETE":
			acks++
			if acks == 1 {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(204)
		case strings.Contains(r.URL.Path, "/sessions/") && r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected session request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	g, err := newGitHub(Connection{Target: "https://github.com/example/repo", Auth: PAT, Credential: SecretRef{Env: "RUNMOOR_TEST_CREDENTIAL"}}, client)
	if err != nil {
		t.Fatal(err)
	}
	session, err := g.Session(context.Background(), PoolState{ScaleSetID: 17}, "fixture-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if session.InitialDemand() != 2 || session.ID() != sessionID {
		t.Fatal("session statistics or identity lost")
	}
	message, err := session.Poll(context.Background(), 0, 3)
	if err != nil || message.MessageID != 7 {
		t.Fatal("poll did not recover expired token", err)
	}
	if err = session.Ack(context.Background(), 7); err != nil {
		t.Fatal("ack did not recover expired token", err)
	}
	mu.Lock()
	if refreshed != 2 || acks != 2 {
		t.Error("SDK refresh paths not exercised")
	}
	revoked = true
	mu.Unlock()
	_, err = session.Poll(context.Background(), 7, 3)
	requireCode(t, err, ErrAuth)
	if strings.Contains(err.Error(), "queue-secret") {
		t.Fatal("queue token leaked")
	}
}
