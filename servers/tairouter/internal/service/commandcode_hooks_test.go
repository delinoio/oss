// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/commandcode"
)

func TestCommandCodeLearningFences(t *testing.T) {
	f := newProviderAcceptance(t, "command-code", "glm-5.2", func(http.ResponseWriter, *http.Request) { t.Error("learning performed network I/O") })
	ctx := context.Background()
	a := &inferenceAdapter{s: f.s}
	p, release, err := a.Admit(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, version, err := f.s.GetPrivateVersion(ctx, f.scope, "provider", f.data.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	in := inference.AdapterInput{Principal: p, Target: inference.Target{Wire: commandcode.Wire, Provider: f.data.Metadata.Id, BaseURL: f.data.Metadata.BaseUrl, Model: "glm-5.2"}, Credential: inference.Credential{AccountID: f.data.Metadata.Id, CredentialVersion: version}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, denied := range []string{"high", "max"} {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			errs <- a.learnCommandCodeEfforts(ctx, in, "zai-org/GLM-5.2", []string{"high", "max"}, []string{v})
		}(denied)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	policy, err := a.commandCodePolicy(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	rejected := policy.RejectedEfforts["zai-org/GLM-5.2"]
	if !slices.Contains(rejected, "high") || !slices.Contains(rejected, "max") || len(policy.RefreshedEfforts["zai-org/GLM-5.2"]) != 0 {
		t.Fatal("concurrent refusals lost")
	}
	if policy.Version != "" {
		t.Fatal("credential generation used as CLI version")
	}
	other := in
	other.Target.Model = "other-model"
	isolated, err := a.commandCodePolicy(ctx, other)
	if err != nil || len(isolated.RejectedEfforts) != 0 {
		t.Fatalf("cross-model learning: %v", err)
	}
	// Rotation invalidates the old account generation, including late capture.
	f.save()
	if _, err = a.commandCodePolicy(ctx, in); err == nil {
		t.Fatal("old credential snapshot read")
	}
	if err = a.learnCommandCodeEfforts(ctx, in, "zai-org/GLM-5.2", nil, []string{"high"}); err == nil {
		t.Fatal("stale learning accepted")
	}
	_, in.Credential.CredentialVersion, err = f.s.GetPrivateVersion(ctx, f.scope, "provider", f.data.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := a.commandCodePolicy(ctx, in)
	if err != nil || len(fresh.RejectedEfforts) != 0 {
		t.Fatalf("new generation inherited old learning: %v", err)
	}
	if err = f.s.RevokeKey(ctx, f.scope, f.keyID); err != nil {
		t.Fatal(err)
	}
	if err = a.learnCommandCodeEfforts(ctx, in, "zai-org/GLM-5.2", nil, []string{"high"}); err == nil {
		t.Fatal("revoked key learned state")
	}
}
