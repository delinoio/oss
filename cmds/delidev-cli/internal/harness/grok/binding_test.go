package grok

import (
	"context"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSessionBindingRequiresOriginalReadyModeAndConfiguration(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, "mode-valid")
			config.Mode = mode
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.sessionBinding(context.Background()); err == nil {
				t.Fatal("uncreated native session supplied binding")
			}
			var creation []CreationClaim
			request, product := domain.NewID(), domain.NewID()
			session, err := api.Create(context.Background(), request, product, func(_ context.Context, c CreationClaim) error { creation = append(creation, c); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if mode == domain.PlanMode {
				if _, err := api.sessionBinding(context.Background()); err == nil {
					t.Fatal("missing Plan selection supplied binding")
				}
				if _, err := api.SelectPlan(context.Background(), domain.NewID(), func(context.Context, ModeClaim) error { return nil }); err != nil {
					t.Fatal(err)
				}
			}
			bound, err := api.sessionBinding(context.Background())
			if err != nil || bound.NativeSessionID != session || bound.ProductSessionID != product || bound.CreationRequestID != request || bound.OwnerID != config.Probe.Process.OwnerID || bound.Model != config.Model || bound.ContextTokens != config.ContextTokens || bound.ConfigurationDigest != creation[1].ConfigurationDigest || (bound.ModeBinding != nil) != (mode == domain.PlanMode) {
				t.Fatal("native binding lost original provenance", err)
			}
			if bound.ModeBinding != nil {
				bound.ModeBinding.EventID = "foreign"
				again, err := api.sessionBinding(context.Background())
				if err != nil || again.ModeBinding.EventID == "foreign" {
					t.Fatal("returned mode mutated native authority")
				}
			}
			original, err := os.ReadFile(api.profile.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(api.profile.path, append(original, []byte("\n# unexpected drift\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := api.sessionBinding(context.Background()); err == nil {
				t.Fatal("configuration drift supplied original binding")
			}
			if err := os.WriteFile(api.profile.path, original, 0600); err != nil {
				t.Fatal(err)
			}
			api.inputStarted = true
			if _, err := api.sessionBinding(context.Background()); err == nil {
				t.Fatal("live input supplied pre-input binding")
			}
			api.inputStarted = false
			if err := api.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := api.sessionBinding(context.Background()); err == nil {
				t.Fatal("closed process supplied native binding")
			}
		})
	}
}
