// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
)

func liveBindingFixture(t *testing.T) (*inferenceAdapter, inference.Principal, core.Scope, providerData, inference.LiveBinding) {
	t.Helper()
	a, sc, key := dispatchFixture(t)
	d := saveRoutingFixture(t, a, sc, "account", "model")
	p := admitRouting(t, a, key)
	_, version, err := a.s.GetPrivateVersion(context.Background(), sc, string(providerKind), d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	b := inference.LiveBinding{
		Target:                  inference.Target{Provider: d.Metadata.Id, BaseURL: d.Metadata.BaseUrl, Model: "wire-model", Wire: inference.Responses, Auth: inference.APIKey},
		AccountID:               d.Metadata.Id,
		UpstreamCallID:          "call_original",
		ServedCredentialVersion: version,
		ExpiresAt:               time.Now().Add(5 * time.Minute),
	}
	return a, p, sc, d, b
}

func TestLiveBindingPublicationGenerationAndEnvelope(t *testing.T) {
	a, p, sc, d, binding := liveBindingFixture(t)
	ctx := context.Background()
	if err := a.SaveBinding(ctx, p, "alias", binding); err != nil {
		t.Fatal(err)
	}
	raw, err := a.s.GetState(ctx, sc, core.Responses, "live/"+p.KeyID+"/alias")
	if err != nil {
		t.Fatal(err)
	}
	var saved storedLiveBinding
	if err = json.Unmarshal(raw, &saved); err != nil || saved.Version != liveBindingEnvelopeVersion || saved.Binding.ServedCredentialVersion != binding.ServedCredentialVersion || saved.Binding.UpstreamCallID != binding.UpstreamCallID || !saved.Binding.ExpiresAt.Equal(binding.ExpiresAt) {
		t.Fatalf("saved binding lacks serving proof: %+v, %v", saved, err)
	}
	d.Secret = "fixture-v2"
	putRoutingProvider(t, a, sc, d)
	if err = a.SaveBinding(ctx, p, "stale", binding); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("replaced account published alias: %v", err)
	}
	if _, err = a.s.GetState(ctx, sc, core.Responses, "live/"+p.KeyID+"/stale"); !errors.Is(err, store.ErrContinuation) {
		t.Fatalf("stale alias became usable: %v", err)
	}
	if _, _, err = a.ResolveBinding(ctx, p, "alias"); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("replacement did not invalidate saved alias: %v", err)
	}
}

func TestLiveBindingPublicationRace(t *testing.T) {
	a, p, sc, d, binding := liveBindingFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var publishErr, replaceErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		publishErr = a.SaveBinding(ctx, p, "race", binding)
	}()
	go func() {
		defer wg.Done()
		<-start
		d.Secret = "fixture-v2"
		raw, err := json.Marshal(d)
		if err == nil {
			replaceErr = a.s.PutPrivate(ctx, sc, string(providerKind), d.Metadata.Id, raw)
		} else {
			replaceErr = err
		}
	}()
	close(start)
	wg.Wait()
	if replaceErr != nil {
		t.Fatal(replaceErr)
	}
	if publishErr != nil && !errors.Is(publishErr, inference.ErrContinuation) {
		t.Fatal(publishErr)
	}
	raw, err := a.s.GetState(ctx, sc, core.Responses, "live/"+p.KeyID+"/race")
	if publishErr != nil {
		if !errors.Is(err, store.ErrContinuation) {
			t.Fatalf("failed publication left state: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var saved storedLiveBinding
	if err = json.Unmarshal(raw, &saved); err != nil || saved.Binding.ServedCredentialVersion != binding.ServedCredentialVersion {
		t.Fatalf("race relabeled the served call: %+v, %v", saved, err)
	}
	if _, _, err = a.ResolveBinding(ctx, p, "race"); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("replacement did not invalidate race winner: %v", err)
	}
}

func TestLiveBindingProoflessEnvelopeAndCredentialMismatch(t *testing.T) {
	a, p, sc, _, binding := liveBindingFixture(t)
	ctx := context.Background()
	if err := a.SaveBinding(ctx, p, "missing", inference.LiveBinding{AccountID: binding.AccountID, ExpiresAt: binding.ExpiresAt}); !errors.Is(err, inference.ErrContinuation) {
		t.Fatalf("missing serving proof accepted: %v", err)
	}
	for name, value := range map[string]any{
		"legacy": struct {
			Binding           inference.LiveBinding
			CredentialVersion string
		}{binding, binding.ServedCredentialVersion},
		"no-proof": storedLiveBinding{Version: liveBindingEnvelopeVersion, Binding: inference.LiveBinding{AccountID: binding.AccountID, ExpiresAt: binding.ExpiresAt}},
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.s.PutState(ctx, sc, core.Responses, "live/"+p.KeyID+"/"+name, raw); err != nil {
			t.Fatal(err)
		}
		if _, _, err = a.ResolveBinding(ctx, p, name); !errors.Is(err, inference.ErrContinuation) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
	if matchesServedCredential(binding, inference.Credential{AccountID: binding.AccountID, CredentialVersion: "new-generation"}) || matchesServedCredential(binding, inference.Credential{AccountID: "other", CredentialVersion: binding.ServedCredentialVersion}) || !matchesServedCredential(binding, inference.Credential{AccountID: binding.AccountID, CredentialVersion: binding.ServedCredentialVersion}) {
		t.Fatal("lookup did not bind selected credentials to served generation and account")
	}
}

func TestLiveOfferRejectsReplacementAfterUpstreamHeaders(t *testing.T) {
	for _, path := range []string{"/v1/realtime/calls", "/v1/live"} {
		t.Run(path, func(t *testing.T) {
			headers := make(chan string, 1)
			resume := make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(resume) }) }
			defer unblock()
			f := newProviderAcceptance(t, "custom", "gpt-live-1-codex", func(w http.ResponseWriter, r *http.Request) {
				served := r.Header.Get("Authorization")
				w.Header().Set("Location", "/v1/realtime/calls/call_original")
				w.Header().Set("Content-Type", "application/sdp")
				w.WriteHeader(http.StatusCreated)
				w.(http.Flusher).Flush()
				headers <- served
				<-resume
				_, _ = io.WriteString(w, "answer")
			})
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"sdp":"offer","session":{"model":"fixture"}}`))
				r.Header.Set("Authorization", "Bearer "+f.key)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				f.srv.handler().ServeHTTP(w, r)
				result <- w
			}()
			select {
			case got := <-headers:
				if got != "Bearer "+f.secret {
					t.Fatalf("offer used a different credential")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("upstream did not flush offer headers")
			}
			f.data.Secret = "fixture-v2"
			f.save()
			unblock()
			select {
			case w := <-result:
				if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
					t.Fatalf("stale offer returned alias: %d %s", w.Code, w.Header().Get("Location"))
				}
			case <-time.After(10 * time.Second):
				t.Fatal("offer did not complete")
			}
			var count int
			if err := f.db.QueryRow(`SELECT count(*) FROM "`+f.schema+`_temp".states WHERE org_id=$1 AND user_id=$2 AND id LIKE $3`, f.scope.OrganizationID, f.scope.UserID, "live/"+f.keyID+"/%").Scan(&count); err != nil || count != 0 {
				t.Fatalf("stale offer persisted an alias: count=%d err=%v", count, err)
			}
		})
	}
}
