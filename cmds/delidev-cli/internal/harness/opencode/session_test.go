package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const fixtureSessionID = "ses_01960dcbe1faabcdefghijklmn"
const fixtureMessageID = "msg_01960dcbe1faABCDEFGHIJKLMN"
const fixturePartID = "prt_01960dcbe1fa1234567890ABCD"

func fixtureSettings() SessionSettings {
	return SessionSettings{Title: "Private title", Agent: "build", Provider: "private-provider", Model: "private-model", Permission: []PermissionRule{{Permission: "*", Pattern: "*", Action: PermissionAsk}}}
}

func fixtureSession(cwd string, request domain.ID, settings SessionSettings) map[string]any {
	return map[string]any{
		"id": fixtureSessionID, "slug": "private-fixture", "projectID": "global", "directory": cwd, "path": "private/fixture", "title": settings.Title,
		"agent": settings.Agent, "model": sessionModel{settings.Model, settings.Provider}, "version": SupportedVersion,
		"metadata": sessionMetadata{sessionMarker{request}}, "permission": settings.Permission,
		"time": map[string]any{"created": 1234, "updated": 1234}, "cost": 0,
		"tokens": map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
	}
}

func fixtureInput(receipt InputReceipt, settings SessionSettings, text string) map[string]any {
	return map[string]any{
		"info":  map[string]any{"id": receipt.MessageID, "sessionID": receipt.SessionID, "role": "user", "time": map[string]any{"created": 1235}, "agent": settings.Agent, "model": inputModel{settings.Provider, settings.Model}, "summary": map[string]any{"diffs": []any{}}},
		"parts": []any{map[string]any{"id": receipt.PartID, "sessionID": receipt.SessionID, "messageID": receipt.MessageID, "type": "text", "text": text}},
	}
}

type sessionFixture struct {
	mu        sync.Mutex
	claims    []SessionClaim
	bodies    [][]byte
	session   map[string]any
	input     map[string]any
	missing   bool
	lost      SessionMutation
	claimFail SessionMutation
	posts     int
	api       *sessionAPI
	logs      bytes.Buffer
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	f := &sessionFixture{}
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		username, password, ok := r.BasicAuth()
		if !ok || username != "delidev" || password != "private-http-credential" || r.Header.Get("x-opencode-directory") != cwd || r.URL.RawQuery != "" {
			t.Error("foreign authority or workspace")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			f.posts++
			body, _ := io.ReadAll(r.Body)
			f.bodies = append(f.bodies, body)
			if len(f.claims) != f.posts || f.claims[f.posts-1].BodyDigest != mutationDigest(body) {
				t.Error("mutation preceded its exact synchronized claim")
				w.WriteHeader(400)
				return
			}
			claim := f.claims[f.posts-1]
			switch r.URL.Path {
			case "/session":
				f.session = fixtureSession(cwd, claim.RequestID, fixtureSettings())
			case "/session/" + fixtureSessionID + "/prompt_async":
				var input struct {
					MessageID string      `json:"messageID"`
					Model     inputModel  `json:"model"`
					Agent     string      `json:"agent"`
					Parts     []inputPart `json:"parts"`
				}
				if domain.Decode(body, &input) != nil || len(input.Parts) != 1 {
					t.Error("malformed native input")
					w.WriteHeader(400)
					return
				}
				f.input = fixtureInput(InputReceipt{SessionID: claim.SessionID, MessageID: claim.MessageID, PartID: claim.PartID}, fixtureSettings(), input.Parts[0].Text)
			default:
				t.Error("unexpected mutation route")
				w.WriteHeader(400)
				return
			}
			if claim.Kind == f.lost {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			}
			if claim.Kind == SubmitInputMutation {
				w.Header().Del("Content-Type")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		switch r.URL.Path {
		case "/session", "/session/" + fixtureSessionID:
			_ = json.NewEncoder(w).Encode(f.session)
		case "/session/" + fixtureSessionID + "/message/" + fixtureMessageID:
			if f.missing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"diagnostic":"private-error-sentinel"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(f.input)
		default:
			t.Error("unexpected read route")
			w.WriteHeader(400)
		}
	}))
	t.Cleanup(server.Close)
	client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
	t.Cleanup(transport.CloseIdleConnections)
	f.api = &sessionAPI{client: client, origin: server.URL, password: "private-http-credential", cwd: cwd, alive: func() error { return nil }, gate: make(chan struct{}, 1), logger: slog.New(slog.NewJSONHandler(&f.logs, nil)), owner: domain.NewID()}
	f.api.claim = func(_ context.Context, claim SessionClaim) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.claims = append(f.claims, claim)
		if claim.Kind == f.claimFail {
			return errors.New("private-error-sentinel")
		}
		return nil
	}
	return f
}

func (f *sessionFixture) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts
}

func (f *sessionFixture) setMissing(value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing = value
}

func (f *sessionFixture) create(t *testing.T) {
	t.Helper()
	id, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings())
	if err != nil || id != fixtureSessionID {
		t.Fatalf("create: %v", err)
	}
}

func TestSessionOriginalClaimsAndInputStorage(t *testing.T) {
	f := newSessionFixture(t)
	f.create(t)
	if id, err := f.api.inspectSession(context.Background()); err != nil || id != fixtureSessionID {
		t.Fatalf("inspect: %v", err)
	}
	text := "private-prompt-sentinel"
	receipt, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, text)
	if err != nil || !receipt.HTTPAccepted || receipt.Recorded || receipt.SessionID != fixtureSessionID || receipt.MessageID != fixtureMessageID || receipt.PartID != fixturePartID {
		t.Fatalf("submit: %+v, %v", receipt, err)
	}
	f.setMissing(true)
	absent, err := f.api.inspectInput(context.Background())
	if err != nil || absent != receipt {
		t.Fatalf("absence changed acceptance: %+v, %v", absent, err)
	}
	f.setMissing(false)
	stored, err := f.api.inspectInput(context.Background())
	if err != nil || !stored.Recorded || !stored.HTTPAccepted {
		t.Fatalf("storage: %+v, %v", stored, err)
	}
	f.setMissing(true)
	lost, err := f.api.inspectInput(context.Background())
	if err == nil || lost != stored {
		t.Fatal("disappearance erased original evidence")
	}
	if _, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, text); err == nil {
		t.Fatal("storage observation permitted another send")
	}
	if _, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || f.postCount() != 2 {
		t.Fatal("scope permitted another creation")
	}
	f.mu.Lock()
	raw, _ := json.Marshal(f.claims)
	f.mu.Unlock()
	for _, private := range []string{text, f.api.cwd, f.api.password, fixtureSettings().Title, "private-error-sentinel"} {
		if bytes.Contains(raw, []byte(private)) || strings.Contains(f.logs.String(), private) {
			t.Fatal("private content escaped into ownership or logs")
		}
	}
}

func TestSessionLostResponsesNeverRetry(t *testing.T) {
	for _, mutation := range []SessionMutation{CreateSessionMutation, SubmitInputMutation} {
		t.Run(string(mutation), func(t *testing.T) {
			f := newSessionFixture(t)
			f.lost = mutation
			if mutation == CreateSessionMutation {
				if _, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("lost creation response not retained")
				}
				if _, err := f.api.create(context.Background(), domain.NewID(), fixtureSettings()); err == nil || f.postCount() != 1 {
					t.Fatal("repeated creation after response loss")
				}
				return
			}
			f.create(t)
			receipt, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "private input")
			if err == nil || receipt.HTTPAccepted || receipt.Recorded || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("lost submission response not retained")
			}
			stored, err := f.api.inspectInput(context.Background())
			if err != nil || !stored.Recorded || stored.HTTPAccepted || stored.RequestID != receipt.RequestID {
				t.Fatalf("original storage failed reconciliation: %+v, %v", stored, err)
			}
			if _, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "private input"); err == nil || f.postCount() != 2 {
				t.Fatal("repeated submission after response loss")
			}
		})
	}
}

func TestSessionClaimFailureAndCancellationPreventSend(t *testing.T) {
	for _, mutation := range []SessionMutation{CreateSessionMutation, SubmitInputMutation} {
		for _, cancelClaim := range []bool{false, true} {
			f := newSessionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			if mutation == SubmitInputMutation {
				f.create(t)
			}
			prior := f.postCount()
			if cancelClaim {
				original := f.api.claim
				f.api.claim = func(ctx context.Context, claim SessionClaim) error {
					err := original(ctx, claim)
					cancel()
					return err
				}
			} else {
				f.mu.Lock()
				f.claimFail = mutation
				f.mu.Unlock()
			}
			var err error
			if mutation == CreateSessionMutation {
				_, err = f.api.create(ctx, domain.NewID(), fixtureSettings())
			} else {
				_, err = f.api.submit(ctx, domain.NewID(), fixtureMessageID, fixturePartID, "private input")
			}
			cancel()
			if err == nil || f.postCount() != prior || strings.Contains(err.Error(), "private-error-sentinel") || strings.Contains(f.logs.String(), "private-error-sentinel") {
				t.Fatalf("unclaimed send or leaked failure: %v", err)
			}
		}
	}
}

func fixtureObject(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestSessionRejectsChangedNativeSettingsAndMetadata(t *testing.T) {
	for _, key := range []string{"id", "slug", "projectID", "directory", "path", "title", "agent", "model", "version", "metadata", "time", "permission", "cost", "tokens"} {
		for _, mutation := range []string{"missing", "null", "alias"} {
			t.Run(key+"/"+mutation, func(t *testing.T) {
				creation := &sessionCreation{request: domain.NewID(), settings: fixtureSettings()}
				value := fixtureObject(t, fixtureSession("/private/workspace", creation.request, creation.settings))
				switch mutation {
				case "missing":
					delete(value, key)
				case "null":
					value[key] = nil
				case "alias":
					value[strings.ToUpper(key)] = value[key]
					delete(value, key)
				}
				raw, _ := json.Marshal(value)
				if _, err := validateSession(raw, "/private/workspace", creation, true); err == nil {
					t.Fatal("invalid native evidence accepted")
				}
			})
		}
	}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["model"].(map[string]any)["providerID"] = "foreign" },
		func(v map[string]any) { v["model"].(map[string]any)["variant"] = "foreign" },
		func(v map[string]any) {
			v["metadata"].(map[string]any)["delidev"].(map[string]any)["request_id"] = string(domain.NewID())
		},
		func(v map[string]any) { v["permission"].([]any)[0].(map[string]any)["action"] = "allow" },
		func(v map[string]any) { v["permission"].([]any)[0].(map[string]any)["Pattern"] = "*" },
		func(v map[string]any) { v["time"].(map[string]any)["archived"] = 1234 },
		func(v map[string]any) { v["tokens"].(map[string]any)["input"] = -1 },
		func(v map[string]any) { v["share"] = map[string]any{"url": "private-locator"} },
		func(v map[string]any) { v["parentID"] = fixtureSessionID },
		func(v map[string]any) { v["cost"] = "0" },
	} {
		creation := &sessionCreation{request: domain.NewID(), settings: fixtureSettings()}
		value := fixtureObject(t, fixtureSession("/private/workspace", creation.request, creation.settings))
		mutate(value)
		raw, _ := json.Marshal(value)
		if _, err := validateSession(raw, "/private/workspace", creation, true); err == nil {
			t.Fatal("changed native settings accepted")
		}
	}
}

func TestNativeSessionCreationPreservesIndependentClockReads(t *testing.T) {
	creation := &sessionCreation{request: domain.NewID(), settings: fixtureSettings()}
	for _, updated := range []int{1233, 1234, 1235} {
		value := fixtureSession("/private/workspace", creation.request, creation.settings)
		value["time"] = map[string]any{"created": 1234, "updated": updated}
		raw, _ := json.Marshal(value)
		identity, err := validateSession(raw, "/private/workspace", creation, true)
		if (err == nil) != (updated >= 1234) || err == nil && identity.created != 1234 {
			t.Fatal("independent creation timestamps changed original ownership or accepted regression")
		}
	}
}

func TestStoredInputPreservesExactOriginalOwnership(t *testing.T) {
	input := sessionInput{receipt: InputReceipt{SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, digest: sha256.Sum256([]byte("private input"))}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["info"].(map[string]any)["id"] = fixturePartID },
		func(v map[string]any) { v["info"].(map[string]any)["sessionID"] = "foreign" },
		func(v map[string]any) { v["info"].(map[string]any)["model"].(map[string]any)["modelID"] = "foreign" },
		func(v map[string]any) { v["info"].(map[string]any)["role"] = "assistant" },
		func(v map[string]any) { v["info"].(map[string]any)["system"] = "foreign instructions" },
		func(v map[string]any) { v["parts"].([]any)[0].(map[string]any)["text"] = "changed" },
		func(v map[string]any) { v["parts"].([]any)[0].(map[string]any)["messageID"] = "foreign" },
		func(v map[string]any) { v["parts"].([]any)[0].(map[string]any)["synthetic"] = true },
		func(v map[string]any) { v["parts"] = append(v["parts"].([]any), v["parts"].([]any)[0]) },
		func(v map[string]any) { v["parts"] = nil },
		func(v map[string]any) { v["info"].(map[string]any)["time"] = map[string]any{"created": nil} },
		func(v map[string]any) { v["info"].(map[string]any)["summary"] = map[string]any{"diffs": nil} },
	} {
		value := fixtureObject(t, fixtureInput(input.receipt, fixtureSettings(), "private input"))
		mutate(value)
		raw, _ := json.Marshal(value)
		if _, err := validateStoredInput(raw, fixtureSettings(), input); err == nil {
			t.Fatal("changed original input accepted")
		}
	}
}

func TestNativeSessionIdentityAndCanceledWait(t *testing.T) {
	for _, id := range []string{fixtureMessageID, strings.ToUpper(fixtureMessageID), "../" + fixtureMessageID, fixtureMessageID + "?", string(domain.NewID())} {
		if nativeID(id, "msg") != (id == fixtureMessageID) {
			t.Fatal("native ID profile mismatch")
		}
	}
	f := newSessionFixture(t)
	f.api.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.api.create(ctx, domain.NewID(), fixtureSettings()); err == nil || f.postCount() != 0 || len(f.claims) != 0 {
		t.Fatal("canceled waiter acquired mutation ownership")
	}
}

func TestSessionContradictionsCannotAuthorizeLaterInput(t *testing.T) {
	f := newSessionFixture(t)
	f.create(t)
	f.mu.Lock()
	f.session["agent"] = "foreign"
	f.mu.Unlock()
	if _, err := f.api.inspectSession(context.Background()); err == nil {
		t.Fatal("changed native agent accepted")
	}
	f.mu.Lock()
	f.session["agent"] = fixtureSettings().Agent
	f.mu.Unlock()
	if _, err := f.api.inspectSession(context.Background()); err != nil {
		t.Fatal("read-only original inspection lost authority")
	}
	if _, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "Must remain blocked"); err == nil || f.postCount() != 1 {
		t.Fatal("later matching read erased prior uncertainty")
	}
}

func TestSessionTransportRoutesAndOwnerLoss(t *testing.T) {
	f := newSessionFixture(t)
	f.create(t)
	for _, route := range []string{"/global/config", "/session/foreign", "/session/" + fixtureSessionID + "?directory=foreign", "/session/" + fixtureSessionID + "/abort", "/session/" + fixtureSessionID + "/fork"} {
		if _, _, err := f.api.request(context.Background(), http.MethodPost, route, []byte(`{}`), 200); err == nil {
			t.Fatal("unowned route accepted")
		}
	}
	original := f.api.claim
	f.api.claim = func(ctx context.Context, claim SessionClaim) error {
		err := original(ctx, claim)
		f.api.alive = func() error { return errors.New("private-native-owner-path") }
		return err
	}
	receipt, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "Must remain unsent")
	if err == nil || receipt.HTTPAccepted || f.postCount() != 1 || strings.Contains(err.Error(), "private-native-owner-path") || strings.Contains(f.logs.String(), "private-native-owner-path") {
		t.Fatal("lost native ownership permitted send or exposed diagnostics")
	}
	if _, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "Must remain unsent"); err == nil {
		t.Fatal("lost ownership released original claim")
	}
}

func TestNativeInputRowBeforePartIsUnconfirmed(t *testing.T) {
	f := newSessionFixture(t)
	f.create(t)
	receipt, err := f.api.submit(context.Background(), domain.NewID(), fixtureMessageID, fixturePartID, "private input")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	parts := f.input["parts"]
	f.input["parts"] = []any{}
	f.mu.Unlock()
	partial, err := f.api.inspectInput(context.Background())
	if err != nil || partial != receipt || partial.Recorded {
		t.Fatalf("intermediate native row misclassified: %+v, %v", partial, err)
	}
	f.mu.Lock()
	f.input["parts"] = parts
	f.mu.Unlock()
	stored, err := f.api.inspectInput(context.Background())
	if err != nil || !stored.Recorded {
		t.Fatal("complete original part did not confirm storage")
	}
	f.mu.Lock()
	f.input["parts"] = []any{}
	f.mu.Unlock()
	regressed, err := f.api.inspectInput(context.Background())
	if err == nil || regressed != stored {
		t.Fatal("regressed parts erased original confirmed storage")
	}
}

func TestOriginalSessionAtGitRootHasEmptyRelativePath(t *testing.T) {
	request := domain.NewID()
	settings := fixtureSettings()
	value := fixtureSession("/private/workspace", request, settings)
	value["path"] = ""
	raw, _ := json.Marshal(value)
	creation := &sessionCreation{request: request, settings: settings}
	if _, err := validateSession(raw, "/private/workspace", creation, true); err != nil {
		t.Fatal("native Git root path was rejected", err)
	}
	delete(value, "path")
	raw, _ = json.Marshal(value)
	if _, err := validateSession(raw, "/private/workspace", creation, true); err == nil {
		t.Fatal("missing native path was accepted")
	}
	value["path"] = nil
	raw, _ = json.Marshal(value)
	if _, err := validateSession(raw, "/private/workspace", creation, true); err == nil {
		t.Fatal("null native path became empty")
	}
}
