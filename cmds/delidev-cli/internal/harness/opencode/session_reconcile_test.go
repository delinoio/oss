package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type creationReadFixture struct {
	mu       sync.Mutex
	f        *sessionFixture
	original http.RoundTripper
	paths    []string
	edit     func(string, any) any
	fail     string
}

type changedCreationResponse struct{ http.RoundTripper }

func (c changedCreationResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := c.RoundTripper.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost && request.URL.Path == "/session" {
		var value map[string]any
		_ = json.NewDecoder(response.Body).Decode(&value)
		_ = response.Body.Close()
		value["model"] = sessionModel{"contradictory-private-model", fixtureSettings().Provider}
		raw, _ := json.Marshal(value)
		response.Body = io.NopCloser(strings.NewReader(string(raw)))
		response.ContentLength = int64(len(raw))
	}
	return response, err
}

func (r *creationReadFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		return r.original.RoundTrip(request)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	path := request.URL.RequestURI()
	r.paths = append(r.paths, path)
	if path == r.fail {
		return nil, errors.New("private read failure")
	}
	r.f.mu.Lock()
	raw, _ := json.Marshal(r.f.session)
	r.f.mu.Unlock()
	var session map[string]any
	_ = json.Unmarshal(raw, &session)
	var value any
	switch path {
	case "/session?limit=2":
		value = []any{session}
	case "/session/" + fixtureSessionID:
		value = session
	case "/session/" + fixtureSessionID + "/message?limit=1":
		value = []any{}
	case "/session/status":
		value = map[string]any{}
	default:
		return nil, errors.New("unexpected creation reconciliation route")
	}
	if r.edit != nil {
		value = r.edit(path, value)
	}
	raw, _ = json.Marshal(value)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func newCreationReconcileFixture(t *testing.T) (*sessionFixture, *creationReadFixture) {
	t.Helper()
	f := newSessionFixture(t)
	f.lost = CreateSessionMutation
	reads := &creationReadFixture{f: f, original: f.api.client.Transport}
	f.api.client.Transport = reads
	if id, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || id != "" || f.postCount() != 1 {
		t.Fatal("fixture did not lose the original creation response")
	}
	return f, reads
}

func TestCreationReconciliationPreservesOriginalClaimAndLostAcknowledgment(t *testing.T) {
	f, reads := newCreationReconcileFixture(t)
	api := &OwnedAPI{session: f.api, reading: make(chan struct{}, 1)}
	receipt, err := api.InspectSession(context.Background())
	if err != nil || receipt.RequestID != f.api.creation.request || receipt.SessionID != fixtureSessionID || !receipt.Recorded || receipt.HTTPAccepted || f.postCount() != 1 || len(f.claims) != 1 {
		t.Fatal("storage reconciliation replaced original claim or HTTP evidence")
	}
	want := []string{"/session?limit=2", "/session/" + fixtureSessionID + "/message?limit=1", "/session/status", "/session/" + fixtureSessionID}
	if !slices.Equal(reads.paths, want) || f.api.creationLookup || f.api.creationCandidate != "" {
		t.Fatal("reconciliation changed original read sequence or retained temporary routes")
	}
	if again, err := api.InspectSession(context.Background()); err != nil || again != receipt || len(reads.paths) != 5 {
		t.Fatal("original stored-session inspection changed the receipt")
	}
	if _, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || f.postCount() != 1 {
		t.Fatal("reconciliation granted another creation")
	}
	if _, _, err := f.api.request(context.Background(), http.MethodGet, "/session?limit=2", nil, http.StatusOK); err == nil || len(reads.paths) != 5 {
		t.Fatal("inventory authority escaped reconciliation")
	}
	// Reconciliation allows only the still-unsent original first input. This
	// direct transport fixture does not grant an event subscription or dispatch.
	input, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "original unsent input")
	if err != nil || !input.HTTPAccepted || f.postCount() != 2 || len(f.claims) != 2 {
		t.Fatal("confirmed original creation lost its first-input identity")
	}
	for _, private := range []string{f.api.cwd, f.api.password, fixtureSettings().Title, "original unsent input"} {
		if strings.Contains(f.logs.String(), private) {
			t.Fatal("creation reconciliation disclosed private data")
		}
	}
}

func TestCreationReconciliationRetainsMissingAndTransientEvidence(t *testing.T) {
	for _, stage := range []string{"empty", "inventory", "history", "status", "identity"} {
		t.Run(stage, func(t *testing.T) {
			f, reads := newCreationReconcileFixture(t)
			if stage == "empty" {
				reads.edit = func(path string, value any) any {
					if path == "/session?limit=2" {
						return []any{}
					}
					return value
				}
			} else {
				reads.fail = map[string]string{"inventory": "/session?limit=2", "history": "/session/" + fixtureSessionID + "/message?limit=1", "status": "/session/status", "identity": "/session/" + fixtureSessionID}[stage]
			}
			missing, err := f.api.reconcileCreation(context.Background())
			if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || missing.Recorded || missing.HTTPAccepted || missing.SessionID != "" || f.api.problem != nil || f.api.creationLookup || f.api.creationCandidate != "" || f.postCount() != 1 {
				t.Fatal("missing read evidence became rejection, identity or replay authority")
			}
			reads.edit, reads.fail = nil, ""
			if stored, err := f.api.reconcileCreation(context.Background()); err != nil || !stored.Recorded || stored.HTTPAccepted || f.postCount() != 1 || len(f.claims) != 1 {
				t.Fatal("fresh read did not preserve original attempted creation")
			}
		})
	}
}

func TestCreationReconciliationRejectsContradictionsWithoutAdoptingIdentity(t *testing.T) {
	for _, change := range []string{"null-list", "duplicate", "foreign-marker", "model", "title", "permission", "usage", "summary", "history", "null-history", "busy", "identity-change"} {
		t.Run(change, func(t *testing.T) {
			f, reads := newCreationReconcileFixture(t)
			reads.edit = func(path string, value any) any {
				if path == "/session?limit=2" {
					sessions := value.([]any)
					session := sessions[0].(map[string]any)
					switch change {
					case "null-list":
						return nil
					case "duplicate":
						return append(sessions, session)
					case "foreign-marker":
						session["metadata"] = sessionMetadata{sessionMarker{domain.NewID()}}
					case "model":
						session["model"] = sessionModel{"foreign-model", fixtureSettings().Provider}
					case "title":
						session["title"] = "changed private title"
					case "permission":
						session["permission"] = []any{}
					case "usage":
						session["tokens"].(map[string]any)["input"] = 1
					case "summary":
						session["summary"] = map[string]any{"additions": 0, "deletions": 0, "files": 0}
					}
				}
				if path == "/session/"+fixtureSessionID+"/message?limit=1" {
					if change == "history" {
						return []any{map[string]any{"unexpected": "private conversation"}}
					}
					if change == "null-history" {
						return nil
					}
				}
				if path == "/session/status" && change == "busy" {
					return map[string]any{fixtureSessionID: map[string]any{"type": "busy"}}
				}
				if path == "/session/"+fixtureSessionID && change == "identity-change" {
					value.(map[string]any)["slug"] = "changed-slug"
				}
				return value
			}
			receipt, err := f.api.reconcileCreation(context.Background())
			if err == nil || f.api.problem == nil || receipt.SessionID != "" || receipt.Recorded || f.api.creationCandidate != "" || f.api.creationLookup {
				t.Fatal("contradictory native creation acquired identity authority")
			}
			reads.edit = nil
			count := len(reads.paths)
			if _, err := f.api.reconcileCreation(context.Background()); err == nil || len(reads.paths) != count || f.postCount() != 1 {
				t.Fatal("later valid state erased an original creation contradiction")
			}
		})
	}
}

func TestCreationReconciliationRequiresActualOriginalAttempt(t *testing.T) {
	f := newSessionFixture(t)
	reads := &creationReadFixture{f: f, original: f.api.client.Transport}
	f.api.client.Transport = reads
	if _, err := f.api.reconcileCreation(context.Background()); err == nil || len(reads.paths) != 0 {
		t.Fatal("unclaimed creation performed inventory reads")
	}
	f.claimFail = CreateSessionMutation
	if _, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || f.postCount() != 0 {
		t.Fatal("uncertain claim reached native creation")
	}
	if _, err := f.api.reconcileCreation(context.Background()); err == nil || len(reads.paths) != 0 || f.api.creation.attempted {
		t.Fatal("unsent creation acquired native lookup authority")
	}
}

func TestCreationReconciliationCannotEraseContradictoryOriginalResponse(t *testing.T) {
	f := newSessionFixture(t)
	reads := &creationReadFixture{f: f, original: changedCreationResponse{f.api.client.Transport}}
	f.api.client.Transport = reads
	if id, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || id != "" || f.api.problem == nil || !f.api.creation.acknowledged {
		t.Fatal("contradictory original native response acquired an identity")
	}
	receipt, err := f.api.reconcileCreation(context.Background())
	if err == nil || receipt.Recorded || !receipt.HTTPAccepted || receipt.SessionID != "" || len(reads.paths) != 0 || f.postCount() != 1 {
		t.Fatal("matching later storage erased the original response contradiction")
	}
}

func TestCreationReconciliationSerializesOriginalIdentity(t *testing.T) {
	f, reads := newCreationReconcileFixture(t)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if receipt, err := f.api.reconcileCreation(context.Background()); err != nil || !receipt.Recorded || receipt.SessionID != fixtureSessionID || receipt.HTTPAccepted {
				t.Error("concurrent read changed original receipt")
			}
		}()
	}
	group.Wait()
	if f.postCount() != 1 || len(f.claims) != 1 || len(reads.paths) != 11 {
		t.Fatal("concurrent reconciliation duplicated inventory or native creation")
	}
}
