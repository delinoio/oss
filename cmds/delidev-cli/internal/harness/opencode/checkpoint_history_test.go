package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type checkpointHistoryTransport struct {
	f    *historyFixture
	mode string
}

func (r *checkpointHistoryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/session" {
		return r.f.RoundTrip(request)
	}
	if request.Method != http.MethodGet || request.URL.RawQuery != "limit=2" {
		r.f.t.Fatal("replacement escaped bounded original session inspection")
	}
	r.f.reads = append(r.f.reads, request.URL.RequestURI())
	session := fixtureSession(r.f.o.cwd, r.f.o.creation.request, fixtureSettings())
	sessions := []any{session}
	switch r.mode {
	case "extra-session":
		sessions = append(sessions, session)
	case "missing-session":
		sessions = []any{}
	case "foreign-session":
		session["metadata"] = sessionMetadata{sessionMarker{domain.NewID()}}
	}
	raw, _ := json.Marshal(sessions)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
}

func TestCheckpointHistoryRequiresCompleteOriginalReadOnlyEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "extra-session", "missing-session", "foreign-session", "pending", "busy", "changed-message", "removed-part", "reordered-parts", "changed-part", "extra-part", "foreign-page", "early-end", "invalid-cursor", "duplicate-cursor", "link-only", "extra-history", "repeated-page", "transient"} {
		t.Run(mode, func(t *testing.T) {
			f := newHistoryFixture(t)
			prepareHistoryCleanup(f, func(context.Context) error { return nil })
			if _, err := f.api.CloseCompleted(context.Background()); err != nil {
				t.Fatal(err)
			}
			home := attachCheckpointFixture(t, f.api)
			raw, ref, err := f.api.RetainCheckpoint(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err := decodeCheckpoint(raw, ref, home)
			if err != nil {
				t.Fatal(err)
			}
			original := f.api.session
			f.o.cwd = checkpoint.Workspace
			f.mode, f.reads = mode, nil
			var logs bytes.Buffer
			s := &sessionAPI{creation: original.creation, cwd: checkpoint.Workspace, runtimeRoot: checkpoint.NativeRoot, apiProfile: original.apiProfile, apiVerified: true, gate: make(chan struct{}, 1), owner: domain.NewID(), password: original.password, origin: original.origin, alive: func() error { return nil }, logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			s.client = &http.Client{Transport: &checkpointHistoryTransport{f, mode}}
			err = s.inspectCheckpointHistory(context.Background(), checkpoint, original.apiProfile.Settings.Agent)
			if (err == nil) != (mode == "valid") || s.checkpointRead || s.historyRead != nil || s.input != nil || s.observer != nil || s.events != nil {
				t.Fatal("historical comparison changed live execution or read authority", err)
			}
			if mode != "valid" {
				f.mode = ""
				before := len(f.reads)
				if s.problem == nil || s.inspectCheckpointHistory(context.Background(), checkpoint, original.apiProfile.Settings.Agent) == nil || len(f.reads) != before || !strings.Contains(logs.String(), "opencode_retained_history_comparison_failed") {
					t.Fatal("later matching reads erased original replacement uncertainty")
				}
			}
			for _, private := range []string{"private input", original.password, checkpoint.Workspace, checkpoint.RuntimeHome, original.apiProfile.Token} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("historical comparison logged private state")
				}
			}
			before := len(f.reads)
			if _, _, err := s.request(context.Background(), http.MethodGet, "/session?limit=2", nil, http.StatusOK); err == nil || len(f.reads) != before {
				t.Fatal("historical inspection left broad read authority enabled")
			}
		})
	}
}

func TestCheckpointLineageAndFreshInputRejectOriginalIdentityReuse(t *testing.T) {
	r, _, _ := checkpointStageFixture(t)
	prior := copyHistoryObservation(r.source.History)
	next := copyHistoryObservation(prior)
	next.RequestID = domain.NewID()
	for i := range next.Messages {
		next.Messages[i].ID = strings.Replace(next.Messages[i].ID, "01960d", "02960d", 1)
		for p := range next.Messages[i].Parts {
			next.Messages[i].Parts[p].ID = strings.Replace(next.Messages[i].Parts[p].ID, "prt_0", "prt_1", 1)
		}
	}
	next.InputID, next.AssistantID = next.Messages[0].ID, next.Messages[len(next.Messages)-1].ID
	rehash := func(h *HistoryObservation) {
		h.Digest = ""
		raw, _ := json.Marshal(h)
		h.Digest = mutationDigest(raw)
	}
	rehash(&next)
	value := r.source
	value.Previous, value.History, value.PredecessorSHA256 = []HistoryObservation{prior}, next, r.ref.SHA256
	if !validCheckpointLineage(value) {
		t.Fatal("distinct original lineage was rejected")
	}
	for _, mode := range []string{"request", "message", "part", "parent", "session", "creation"} {
		bad := value
		bad.History = copyHistoryObservation(value.History)
		switch mode {
		case "request":
			bad.History.RequestID = prior.RequestID
		case "message":
			bad.History.Messages[0].ID, bad.History.InputID = prior.InputID, prior.InputID
		case "part":
			bad.History.Messages[0].Parts[0].ID = prior.Messages[0].Parts[0].ID
		case "parent":
			bad.PredecessorSHA256 = ""
		case "session":
			bad.History.SessionID = "ses_02960dcbe1faabcdefghijklmn"
		case "creation":
			bad.History.RequestID = value.Reference.CreationRequestID
		}
		rehash(&bad.History)
		if validCheckpointLineage(bad) {
			t.Fatal("changed lineage was accepted", mode)
		}
	}
	s := &sessionAPI{predecessor: &value, resumeRequest: domain.NewID()}
	if !s.freshCheckpointInput(domain.NewID(), "msg_03960dcbe1faABCDEFGHIJKLMN", "prt_23960dcbe1fa1234567890ABCD") {
		t.Fatal("fresh input was rejected")
	}
	for _, h := range checkpointHistories(value) {
		if s.freshCheckpointInput(h.RequestID, "", "") || s.freshCheckpointInput(domain.NewID(), h.InputID, "") || s.freshCheckpointInput(domain.NewID(), "", h.Messages[0].Parts[0].ID) || s.freshCheckpointInput(s.resumeRequest, "", "") {
			t.Fatal("original request/message/part gained replay authority")
		}
	}
}

func TestLateNativePruningReadIsAtomicAndCannotChangeOriginalContent(t *testing.T) {
	for _, mode := range []string{"valid", "changed-output", "changed-info", "before-completion", "foreign-part", "no-original-inventory"} {
		t.Run(mode, func(t *testing.T) {
			f := newObserverFixture(t)
			info, _ := json.Marshal(f.a)
			expected := HistoryMessage{ID: f.a["id"].(string), Role: AssistantMessageRole, Digest: mutationDigest(canonicalNative(info)), Parts: []HistoryPart{}}
			values := []any{}
			for i := 0; i < 2; i++ {
				original := f.assistantPart(ToolPartKind, 200+i, map[string]any{"tool": "bash", "callID": "private-call", "state": map[string]any{"status": "completed", "input": map[string]any{}, "output": "private-original-output", "title": "private-title", "metadata": map[string]any{}, "time": map[string]any{"start": 1240, "end": 1245}}})
				raw, _ := json.Marshal(original)
				expected.Parts = append(expected.Parts, HistoryPart{ID: original["id"].(string), Kind: ToolPartKind, Digest: mutationDigest(canonicalNative(raw))})
				pruned := contextFixtureCopy(original)
				state := pruned["state"].(map[string]any)
				state["time"].(map[string]any)["compacted"] = 1250
				if i == 1 {
					switch mode {
					case "changed-output":
						state["output"] = "private-changed-output"
					case "foreign-part":
						pruned["id"] = contextFixturePart(900)
					}
				}
				values = append(values, pruned)
			}
			f.o.contextBaseInventory = []HistoryMessage{expected}
			f.o.progress.SettledObserved = mode != "before-completion"
			if mode == "no-original-inventory" {
				f.o.contextBaseInventory = nil
			}
			if mode == "changed-info" {
				f.a["modelID"] = "foreign-model"
				info, _ = json.Marshal(f.a)
			}
			raw, _ := json.Marshal(map[string]any{"info": json.RawMessage(info), "parts": values})
			session := &sessionAPI{observer: f.o, input: &f.o.input}
			if session.checkpointNativeMessageMatches(raw, expected) != (mode == "valid") {
				t.Fatal("late pruning read granted unrelated history authority")
			}
			want := 0
			if mode == "valid" {
				want = 2
			}
			if len(f.o.contextPruned) != want || len(f.o.parts) != 0 || len(f.o.messageOrder) != 0 {
				t.Fatal("partial read published pruning or canonical content")
			}
		})
	}
}
