// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexChildModelSnapshotDoesNotFollowAgentOrCatalogEdits(t *testing.T) {
	f := newFirstDispatchFixture(t)
	account := accountBody(t, f.account)
	child := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{Name: "Child", NativeID: "child-model", ProviderID: account.ProviderID, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})
	f.mutateAgent(t, func(a *domain.Agent) {
		a.Options.SubagentModel = "child-model"
		a.Options.SubagentEffort = "low"
		a.Options.MaxConcurrency = 2
	})
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.child-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, domain.ID(f.machine.Id))
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexSubagentConfigurationV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Decode[domain.Session](f.refresh(t))
	c := before.InitialExecution.Configuration
	if c.SubagentModel == nil || c.SubagentModel.ModelID != domain.ID(child.Id) || c.SubagentModel.ModelRevision != child.Revision || c.Options.MaxConcurrency != 2 || c.Options.SubagentEffort != "low" {
		t.Fatal("child model was not pinned with the original snapshot")
	}
	f.mutateAgent(t, func(a *domain.Agent) {
		a.Options.SubagentModel = "unrelated"
		a.Options.SubagentEffort = "high"
		a.Options.MaxConcurrency = 8
	})
	after, _ := store.Decode[domain.Session](f.refresh(t))
	a, _ := json.Marshal(before.InitialExecution)
	b, _ := json.Marshal(after.InitialExecution)
	if string(a) != string(b) {
		t.Fatal("later Agent edit rewrote child settings")
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.child-catalog-edit", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.ModelKind, domain.ID(child.Id))
		if e != nil {
			return nil, e
		}
		m, e := store.Decode[domain.Model](r)
		if e != nil {
			return nil, e
		}
		m.NativeID = "different-model"
		return tx.Put(domain.ModelKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error { return tx.RequireCodexSubagentModel(c, account) })
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("catalog rename substituted the original child authority", err)
	}
}

func TestCodexChildModelRequiresSameAccountAndWorkerCapabilityBeforeClaim(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		f := newFirstDispatchFixture(t)
		account := accountBody(t, f.account)
		provider := account.ProviderID
		if foreign {
			value := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Foreign", Endpoint: "https://example.invalid", Protocol: domain.OpenAIResponses, Authentication: domain.BearerAuth, Enabled: new(true)})
			provider = domain.ID(value.Id)
		}
		f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{Name: "Child", NativeID: "child-model", ProviderID: provider, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})
		f.mutateAgent(t, func(a *domain.Agent) { a.Options.SubagentModel = "child-model" })
		err := f.service.dispatchExecution(context.Background(), f.refresh(t))
		if domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("unproved child configuration was admitted", err)
		}
		s, _ := store.Decode[domain.Session](f.refresh(t))
		if s.InitialExecution != nil || s.ActiveExecutionID != "" {
			t.Fatal("rejected child selection acquired an execution")
		}
	}
}

func TestCodexChildRelayPinsModelReferencesDiagnosticsAndOriginalAccount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "resp_child_fixture", "model": "child-model", "output": []any{}})
	}))
	defer upstream.Close()
	childID := domain.NewID()
	f := newConfiguredAuthorityFixture(t, upstream.URL, func(i *domain.ExecutionJobInput) {
		i.Configuration.Options.SubagentModel = "child-model"
		i.Configuration.SubagentModel = &domain.ExecutionSubagentModel{ModelID: childID, ModelRevision: 1, NativeModel: "child-model"}
		i.ConfigurationDigest, _ = i.Configuration.Digest()
	}, false)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.child-relay-model", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.ModelKind, childID, 0, "", "", domain.Model{Name: "Child", NativeID: "child-model", ProviderID: f.input.Configuration.ProviderID, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})
	})
	if err != nil {
		t.Fatal(err)
	}
	f.registerGrant(t)
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	child, err := lease.BindModel(context.Background(), "child-model", apiproxy.ResponseCreate)
	if err != nil || child.Scope.AccountID != lease.Scope.AccountID || child.Scope.ConnectionID != lease.Scope.ConnectionID || child.Scope.ModelID != childID {
		t.Fatal("child model gained another account or lost its identity", err)
	}
	if _, err := lease.BindModel(context.Background(), "foreign-model", apiproxy.ResponseCreate); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("relay widened the immutable model set", err)
	}
	if err := child.ObserveReference(context.Background(), apiproxy.ResponseReference, "original-child-response"); err != nil {
		t.Fatal(err)
	}
	if err := lease.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "original-child-response"); err == nil {
		t.Fatal("child native response became root-owned history")
	}
	if err := child.AuthorizeReference(context.Background(), apiproxy.ResponseReference, "original-child-response"); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, f.token, `{"model":"child-model","input":[]}`)
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("configured child relay rejected: %d", response.StatusCode)
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		rows, _, err := tx.ListRequestDiagnostics(f.input.SessionID, f.input.ExecutionID, "", 20)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].ModelID != childID || rows[0].AccountID != f.input.AccountID || rows[0].ConnectionID != f.input.ConnectionID || rows[0].HTTPAttempted == nil || !*rows[0].HTTPAttempted {
			t.Fatal("HTTP child attempt lost original account/model diagnostics")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root := f.input.Configuration.NativeModel
	if err := f.input.Configuration.ValidateCodexChildModels([]domain.SubagentObservation{{RequestedModel: &root, ObservedModel: nil}}); err != nil {
		t.Fatal("an omitted observation became the configured child default")
	}
	foreign := "foreign-model"
	if err := f.input.Configuration.ValidateCodexChildModels([]domain.SubagentObservation{{ObservedModel: &foreign}}); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("foreign native child model accepted", err)
	}
}
