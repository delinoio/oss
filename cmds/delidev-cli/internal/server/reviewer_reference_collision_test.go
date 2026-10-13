// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

type reviewerCollisionRole string

const (
	collisionRoot     reviewerCollisionRole = "root"
	collisionChild    reviewerCollisionRole = "child"
	collisionDistinct reviewerCollisionRole = "distinct"
)

type reviewerCollision struct {
	fixture *authorityFixture
	lease   *apiproxy.Lease
	childID domain.ID
}

// newReviewerCollision configures AI auto-review so the canonical reviewer
// model collides with the root, with the child, or with neither.
func newReviewerCollision(t *testing.T, role reviewerCollisionRole, upstream string) *reviewerCollision {
	t.Helper()
	result := &reviewerCollision{}
	configure := func(i *domain.ExecutionJobInput) {
		i.Configuration.Options.ApprovalsReviewer = domain.CodexReviewerAuto
		i.Configuration.Options.ApprovalPolicy = "on-request"
		i.Configuration.ReviewerNativeModel = domain.CodexReviewerNativeModel
		childNative := "child-model"
		if role == collisionChild {
			childNative = domain.CodexReviewerNativeModel
		}
		result.childID = (domain.ModelIdentity{ProviderID: i.Configuration.ProviderID, NativeID: childNative}).Key()
		i.Configuration.Options.SubagentModel = childNative
		i.Configuration.SubagentModel = &domain.ExecutionSubagentModel{ModelID: result.childID, ModelRevision: 1, NativeModel: childNative}
		i.ConfigurationDigest, _ = i.Configuration.Digest()
	}
	rootNative := "fixture-model"
	if role == collisionRoot {
		rootNative = domain.CodexReviewerNativeModel
	}
	f := newProfileAuthorityFixture(t, upstream, domain.Codex, domain.OpenAIResponses, configure, false, rootNative)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.reviewer-machine", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexApprovalReviewV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registerGrant(t)
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Release)
	result.fixture, result.lease = f, lease
	return result
}

func (c *reviewerCollision) hasReference(t *testing.T, ref store.ExecutionReference, nativeID string) bool {
	t.Helper()
	ref.SessionID, ref.AccountID, ref.ConnectionID = c.fixture.input.SessionID, c.fixture.input.AccountID, c.fixture.input.ConnectionID
	ref.Kind, ref.NativeID = domain.NativeResponseReference, nativeID
	var exists bool
	err := c.fixture.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		exists, err = tx.HasExecutionReference(ref)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

func observeIfPermitted(t *testing.T, lease *apiproxy.Lease, id string) {
	t.Helper()
	if lease.ObserveReference == nil {
		return
	}
	if err := lease.ObserveReference(context.Background(), apiproxy.ResponseReference, id); err != nil {
		t.Fatal(err)
	}
}

func TestReviewerRootModelCollisionGrantsNoReferenceAuthority(t *testing.T) {
	c := newReviewerCollision(t, collisionRoot, "http://127.0.0.1:1")
	ambiguous, err := c.lease.BindModel(context.Background(), domain.CodexReviewerNativeModel, apiproxy.ResponseCreate)
	if err != nil {
		t.Fatal(err)
	}
	// The original root scope, credential and gates are retained.
	if ambiguous.Scope.ModelID != c.lease.Scope.ModelID || ambiguous.Scope.AccountID != c.lease.Scope.AccountID || ambiguous.Scope.Attribution != "" || ambiguous.BindModel != nil {
		t.Fatal("ambiguous root request lost its original lease scope")
	}
	observeIfPermitted(t, ambiguous, "resp_ambiguous_root")
	for name, lease := range map[string]*apiproxy.Lease{"ambiguous": ambiguous, "root": c.lease} {
		if err := lease.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "resp_ambiguous_root"); err == nil {
			t.Fatalf("%s lease authorized an ambiguous reviewer response", name)
		}
	}
	if c.hasReference(t, store.ExecutionReference{ModelID: c.lease.Scope.ModelID}, "resp_ambiguous_root") || c.hasReference(t, store.ExecutionReference{Reviewer: true}, "resp_ambiguous_root") {
		t.Fatal("ambiguous response was recorded as root or reviewer history")
	}
	// The ambiguous lease cannot even reference a previously recorded root response.
	if err := c.lease.ObserveReference(context.Background(), apiproxy.ResponseReference, "resp_ordinary_root"); err != nil {
		t.Fatal(err)
	}
	if err := ambiguous.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "resp_ordinary_root"); err == nil {
		t.Fatal("ambiguous request gained root reference authority")
	}
}

func TestReviewerRootModelCollisionRelaysWithoutRecordingResponseHistory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "resp_collision_relay", "model": domain.CodexReviewerNativeModel, "output": []any{}})
	}))
	defer upstream.Close()
	c := newReviewerCollision(t, collisionRoot, upstream.URL)
	response := c.fixture.request(t, c.fixture.token, `{"model":"gpt-5.6-luna","input":[]}`)
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("plain ambiguous request must keep relaying: %d", response.StatusCode)
	}
	if c.hasReference(t, store.ExecutionReference{ModelID: c.lease.Scope.ModelID}, "resp_collision_relay") || c.hasReference(t, store.ExecutionReference{Reviewer: true}, "resp_collision_relay") {
		t.Fatal("ambiguous response entered root or reviewer history")
	}
	referenced := c.fixture.request(t, c.fixture.token, `{"model":"gpt-5.6-luna","previous_response_id":"resp_collision_relay","input":[]}`)
	_, _ = io.Copy(io.Discard, referenced.Body)
	referenced.Body.Close()
	if referenced.StatusCode == http.StatusOK {
		t.Fatal("ambiguous response authorized a later reference")
	}
}

func TestReviewerChildModelCollisionDoesNotEnterChildHistory(t *testing.T) {
	c := newReviewerCollision(t, collisionChild, "http://127.0.0.1:1")
	ambiguous, err := c.lease.BindModel(context.Background(), domain.CodexReviewerNativeModel, apiproxy.ResponseCreate)
	if err != nil {
		t.Fatal(err)
	}
	if ambiguous.Scope.ModelID != c.childID || ambiguous.Scope.Attribution != "" {
		t.Fatal("shared child/reviewer name changed the original child scope")
	}
	observeIfPermitted(t, ambiguous, "resp_ambiguous_child")
	if err := ambiguous.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "resp_ambiguous_child"); err == nil {
		t.Fatal("ambiguous child request authorized its own response")
	}
	if err := c.lease.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "resp_ambiguous_child"); err == nil {
		t.Fatal("ambiguous response became root history")
	}
	if c.hasReference(t, store.ExecutionReference{ModelID: c.childID}, "resp_ambiguous_child") || c.hasReference(t, store.ExecutionReference{Reviewer: true}, "resp_ambiguous_child") {
		t.Fatal("ambiguous response entered child or reviewer history")
	}
}

func TestReviewerDistinctModelNamesKeepOrdinaryReferencesAndIsolatedReviewerSlot(t *testing.T) {
	c := newReviewerCollision(t, collisionDistinct, "http://127.0.0.1:1")
	child, err := c.lease.BindModel(context.Background(), "child-model", apiproxy.ResponseCreate)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := c.lease.BindModel(context.Background(), domain.CodexReviewerNativeModel, apiproxy.ResponseCreate)
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.Scope.Attribution != domain.BuiltinReviewerAttribution || reviewer.Scope.ModelID != "" || reviewer.AuthorizeReference == nil || reviewer.ObserveReference == nil {
		t.Fatal("distinct reviewer lost its builtin attribution")
	}
	if _, err := c.lease.BindModel(context.Background(), domain.CodexReviewerNativeModel, apiproxy.ResponseCompact); err == nil {
		t.Fatal("reviewer gained compaction")
	}
	for id, lease := range map[string]*apiproxy.Lease{"resp_root": c.lease, "resp_child": child, "resp_reviewer": reviewer} {
		if err := lease.ObserveReference(context.Background(), apiproxy.ResponseReference, id); err != nil {
			t.Fatal(err)
		}
		if err := lease.AuthorizeReference(context.Background(), apiproxy.ResponseReference, id); err != nil {
			t.Fatalf("ordinary reference %s lost authority: %v", id, err)
		}
	}
	for id, other := range map[string][]*apiproxy.Lease{"resp_root": {child, reviewer}, "resp_child": {c.lease, reviewer}, "resp_reviewer": {c.lease, child}} {
		for _, lease := range other {
			if err := lease.AuthorizeReference(context.Background(), apiproxy.ResponseReference, id); err == nil {
				t.Fatalf("%s crossed role boundaries", id)
			}
		}
	}
}
