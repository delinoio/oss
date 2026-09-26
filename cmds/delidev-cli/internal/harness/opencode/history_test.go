package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type historyFixture struct {
	t     *testing.T
	o     *inputObserver
	api   *OwnedAPI
	reads []string
	mode  string
}

func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	observed := newObserverFixture(t)
	observed.start()
	observed.finish(FinishStop)
	observed.status(NativeStatusIdle)
	observed.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	f := &historyFixture{t: t, o: observed.o}
	o := f.o
	creation, input := o.creation, o.input
	session := &sessionAPI{creation: &creation, input: &input, observer: o, events: &eventStream{ctx: context.Background()}, gate: make(chan struct{}, 1), cwd: o.cwd, runtimeRoot: o.root, password: "private-http-secret", origin: "http://127.0.0.1:1", alive: func() error { return nil }}
	session.client = &http.Client{Transport: f}
	f.api = &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	return f
}

func verifyNativeObservedHistory(t *testing.T, ctx context.Context, session *sessionAPI, observer *inputObserver) {
	t.Helper()
	api := &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	proof, err := api.InspectHistory(ctx)
	if err != nil || len(proof.Messages) != len(observer.messages) || proof.AssistantID != observer.progress.AssistantID || proof.InputID != observer.input.receipt.MessageID || len(proof.Digest) != 64 {
		t.Fatalf("native stored tool/interaction history differs from its original observation: %v", err)
	}
}

func historyCursor(id string) string {
	raw, _ := json.Marshal(map[string]any{"id": id, "time": 1235})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (f *historyFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	path := request.URL.RequestURI()
	if request.Method != http.MethodGet {
		f.t.Error("history inspection attempted a native mutation")
	}
	f.reads = append(f.reads, path)
	header := http.Header{"Content-Type": []string{"application/json"}}
	var value any
	switch request.URL.Path {
	case "/session/" + fixtureSessionID:
		value = fixtureSession(f.o.cwd, f.o.creation.request, fixtureSettings())
	case "/session/" + fixtureSessionID + "/message/" + fixtureMessageID:
		value = fixtureInput(f.o.input.receipt, fixtureSettings(), "private input")
	case "/permission", "/question":
		value = []any{}
		if f.mode == "pending" {
			value = []any{map[string]any{"private": "unowned pending interaction"}}
		}
	case "/session/status":
		value = map[string]any{}
		if f.mode == "busy" {
			value = map[string]any{fixtureSessionID: map[string]any{"type": "busy"}}
		}
	case "/session/" + fixtureSessionID + "/message":
		if f.mode == "transient" {
			return nil, errors.New("private transport loss")
		}
		index := len(f.o.messageOrder) - 1
		if request.URL.Query().Get("before") != "" {
			index--
		}
		if request.URL.Query().Get("limit") != "1" || index < 0 {
			f.t.Fatal("unexpected bounded history page")
		}
		message := f.o.messages[f.o.messageOrder[index]]
		var info map[string]any
		_ = json.Unmarshal(message.raw, &info)
		var parts []any = []any{}
		for _, id := range message.parts {
			var part map[string]any
			_ = json.Unmarshal(f.o.parts[id].raw, &part)
			parts = append(parts, part)
		}
		if index > 0 {
			header.Set("X-Next-Cursor", historyCursor(message.value.ID))
			// Hostile Link contents are never followed; the validated cursor
			// can be used only on the original authenticated session route.
			header.Set("Link", `<https://foreign.invalid/private>; rel="next"`)
		}
		switch f.mode {
		case "changed-message":
			info["modelID"] = "foreign-model"
		case "removed-part":
			parts = parts[:len(parts)-1]
		case "reordered-parts":
			if len(parts) > 1 {
				parts[0], parts[1] = parts[1], parts[0]
			}
		case "changed-part":
			parts[0].(map[string]any)["private_extension"] = "unobserved content"
		case "extra-part":
			parts = append(parts, parts[0])
		case "foreign-page":
			info["id"] = "msg_01960dcbe1fcABCDEFGHIJKLMN"
		case "early-end":
			header.Del("X-Next-Cursor")
			header.Del("Link")
		case "invalid-cursor":
			header.Set("X-Next-Cursor", "../../foreign")
		case "duplicate-cursor":
			header.Add("X-Next-Cursor", historyCursor(message.value.ID))
		case "link-only":
			header.Del("X-Next-Cursor")
		case "extra-history":
			header.Set("X-Next-Cursor", historyCursor(message.value.ID))
		case "repeated-page":
			if index == 0 {
				info["id"] = f.o.messageOrder[1]
			}
		}
		value = []any{map[string]any{"info": info, "parts": parts}}
	default:
		f.t.Fatal("history inspection escaped its original route")
	}
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func TestHistoryComparesCompleteOriginalOrderWithoutPublishingPayloads(t *testing.T) {
	f := newHistoryFixture(t)
	proof, err := f.api.InspectHistory(context.Background())
	if err != nil || proof.RequestID != f.o.input.receipt.RequestID || proof.SessionID != fixtureSessionID || proof.InputID != fixtureMessageID || proof.AssistantID != f.o.progress.AssistantID || len(proof.Messages) != 2 || len(proof.Messages[1].Parts) != 2 || len(proof.Digest) != 64 {
		t.Fatalf("original stored history was not verified: %v", err)
	}
	if proof.Messages[0].ID != fixtureMessageID || proof.Messages[1].Parts[0].Kind != StepStartPartKind || proof.Messages[1].Parts[1].Kind != StepFinishPartKind || f.api.session.historyRead != nil {
		t.Fatal("history lost original message/part ordering or retained read authority")
	}
	raw, _ := json.Marshal(proof)
	for _, private := range []string{"private input", "private-http-secret", f.o.cwd, f.o.root, fixtureSettings().Model} {
		if strings.Contains(string(raw), private) {
			t.Fatal("history metadata disclosed private payload")
		}
	}
	for _, path := range f.reads {
		if strings.Contains(path, "foreign.invalid") {
			t.Fatal("native Link header became request authority")
		}
	}
	again, err := f.api.InspectHistory(context.Background())
	if err != nil || again.Digest != proof.Digest {
		t.Fatal("stable original history changed its comparison digest")
	}
	proof.Messages[0].Parts[0].ID = "caller-mutated"
	if f.o.messageOrder[0] != fixtureMessageID || f.o.messages[fixtureMessageID].parts[0] != fixturePartID {
		t.Fatal("returned history mutated original observation")
	}
}

func TestHistoryRejectsChangedOrIncompleteNativeState(t *testing.T) {
	for _, mode := range []string{"pending", "busy", "changed-message", "removed-part", "reordered-parts", "changed-part", "extra-part", "foreign-page", "early-end", "invalid-cursor", "duplicate-cursor", "link-only", "extra-history", "repeated-page"} {
		t.Run(mode, func(t *testing.T) {
			f := newHistoryFixture(t)
			f.mode = mode
			proof, err := f.api.InspectHistory(context.Background())
			if err == nil || proof.Digest != "" || len(proof.Messages) != 0 || !f.o.snapshot().NeedsRecovery || f.api.session.historyRead != nil {
				t.Fatal("incomplete history produced comparison evidence")
			}
			f.mode = ""
			before := len(f.reads)
			if _, err := f.api.InspectHistory(context.Background()); err == nil || len(f.reads) != before {
				t.Fatal("later valid history erased the contradiction")
			}
		})
	}
}

func TestHistoryDoesNotFillMissingLiveEvidenceOrClaimCleanup(t *testing.T) {
	for _, missing := range []string{"settled", "error", "identity", "transport", "cancel", "reader"} {
		t.Run(missing, func(t *testing.T) {
			f := newHistoryFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch missing {
			case "settled":
				f.o.progress.SettledObserved = false
			case "error":
				f.o.problem = observerProblem()
			case "identity":
				f.api.session.creation.request = domain.NewID()
			case "transport":
				f.api.session.events.problem = sessionUncertain()
			case "cancel":
				cancel()
			case "reader":
				f.api.reading <- struct{}{}
				cancel()
			}
			if proof, err := f.api.InspectHistory(ctx); err == nil || proof.Digest != "" || len(f.reads) != 0 {
				t.Fatal("missing live authority gained historical reconstruction")
			}
		})
	}
	f := newHistoryFixture(t)
	f.mode = "transient"
	if proof, err := f.api.InspectHistory(context.Background()); err == nil || proof.Digest != "" || f.o.snapshot().NeedsRecovery {
		t.Fatal("transient read rewrote original native evidence")
	}
	f.mode = ""
	if _, err := f.api.InspectHistory(context.Background()); err != nil {
		t.Fatal(err)
	}
}
