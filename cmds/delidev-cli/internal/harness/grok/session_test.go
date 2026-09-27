package grok

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestCreationRetainsOriginalClaimsAndNeverRepeatsUncertainty(t *testing.T) {
	for _, mode := range []string{"valid", "claim-failure", "bind-failure", "session-model", "setup-foreign", "session-timeout", "setup-mcp-early", "setup-mcp-too-early", "setup-mcp-duplicate", "setup-mcp-incomplete"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := api.Close(); err != nil {
					t.Error(err)
				}
			}()
			request, product := domain.NewID(), domain.NewID()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var claims []CreationClaim
			record := func(_ context.Context, claim CreationClaim) error {
				if claim.Validate() != nil || claim.RequestID != request || claim.ProductSessionID != product {
					t.Fatal("invalid original claim")
				}
				claims = append(claims, claim)
				if mode == "session-timeout" {
					time.AfterFunc(50*time.Millisecond, cancel)
				}
				if mode == "setup-mcp-incomplete" && claim.Phase == BindCreation {
					time.AfterFunc(100*time.Millisecond, cancel)
				}
				if mode == "claim-failure" || mode == "bind-failure" && claim.Phase == BindCreation {
					return context.Canceled
				}
				return nil
			}
			native, err := api.Create(ctx, request, product, record)
			if mode == "valid" || mode == "setup-mcp-early" {
				if err != nil || native.Validate() != nil || len(claims) != 2 || claims[1].NativeSessionID != native || claims[0].BodyDigest != claims[1].BodyDigest || claims[0].ConfigurationDigest != claims[1].ConfigurationDigest {
					t.Fatal("native ownership missing", err)
				}
			} else if err == nil {
				t.Fatal("uncertain session accepted")
			}
			expected := 2
			if mode == "claim-failure" || mode == "session-model" || mode == "session-timeout" {
				expected = 1
			}
			if len(claims) != expected {
				t.Fatalf("claim count %d, expected %d", len(claims), expected)
			}
			if _, err := api.Create(context.Background(), domain.NewID(), product, func(context.Context, CreationClaim) error { t.Error("uncertain creation repeated"); return nil }); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("creation boundary reopened", err)
			}
		})
	}
}

func TestConcurrentCreationCanBindOnlyOneNativeSession(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "valid")
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	claims := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { claims++; return nil })
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	completed := 0
	for err := range results {
		if err == nil {
			completed++
		} else if domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal(err)
		}
	}
	if completed != 1 || claims != 2 {
		t.Fatal("duplicate native session creation")
	}
}

func TestInitializedConfigurationPermitsOnlyOriginalNativePurgeMarker(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "valid")
	profile, err := buildAPIProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		suffix string
		valid  bool
	}{
		{"", true},
		{"\n[marketplace]\ndefault_skills_installs_purged=true\n", true},
		{"\n[marketplace]\ndefault_skills_installs_purged=false\n", false},
		{"\n[marketplace]\ndefault_skills_installs_purged=true\nother=true\n", false},
		{"\n[marketplace]\ndefault_skills_installs_purged=true\n[hooks]\ncommand='foreign'\n", false},
	} {
		if err := os.WriteFile(profile.path, append(append([]byte{}, profile.configuration...), test.suffix...), 0600); err != nil {
			t.Fatal(err)
		}
		if (profile.checkInitialized() == nil) != test.valid {
			t.Fatal("configuration rewrite classification changed")
		}
	}
}

func TestSessionEffectiveIdentityRejectsEveryConflictingSurface(t *testing.T) {
	profile := apiProfile{contextTokens: 32000}
	if _, err := validateNewSession(sessionFixture, "/private/workspace", profile); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["sessionId"] = domain.NewID() },
		func(v map[string]any) { v["models"].(map[string]any)["currentModelId"] = "foreign" },
		func(v map[string]any) { v["configOptions"].([]any)[0].(map[string]any)["currentValue"] = "foreign" },
		func(v map[string]any) { v["_meta"].(map[string]any)["currentWorkingDirectory"] = "/foreign" },
		func(v map[string]any) { v["_meta"].(map[string]any)["isGitRepo"] = true },
		func(v map[string]any) {
			v["_meta"].(map[string]any)["x.ai/sessionDetail"].(map[string]any)["currentModelId"] = "foreign"
		},
		func(v map[string]any) {
			v["_meta"].(map[string]any)["x.ai/sessionConfig"].(map[string]any)["options"].([]any)[0].(map[string]any)["selected"] = false
		},
	} {
		value := fixtureObject(sessionFixture)
		mutate(value)
		raw, _ := json.Marshal(value)
		if _, err := validateNewSession(raw, "/private/workspace", profile); err == nil {
			t.Fatal("conflicting effective session accepted")
		}
	}
}
