package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInitialSettingsNeedOwnedContextAndExactNativeDefaults(t *testing.T) {
	for _, change := range []string{"none", "unverified", "missing-profile", "missing-context", "nil-permission", "custom-permission", "dead-owner", "missing-owner", "contradiction"} {
		t.Run(change, func(t *testing.T) {
			profile := fixtureAPIProfile()
			profile.Settings.Agent = PlanAgent
			profile.Settings.Permission = []PermissionRule{}
			f := newSessionFixture(t)
			api := f.api
			api.apiProfile, api.apiVerified, api.runtimeRoot = profile, true, f.api.cwd
			switch change {
			case "unverified":
				api.apiVerified = false
			case "missing-profile":
				api.apiProfile = nil
			case "missing-context":
				api.runtimeRoot = ""
			case "nil-permission":
				profile.Settings.Permission = nil
			case "custom-permission":
				profile.Settings.Permission = fixtureSettings().Permission
			case "dead-owner":
				api.alive = func() error { return unavailable() }
			case "missing-owner":
				api.alive = nil
			case "contradiction":
				api.problem = sessionProblem()
			}
			observed, err := api.initialObservedSettings(context.Background())
			if (err == nil) != (change == "none") || f.postCount() != 0 || len(f.claims) != 0 {
				t.Fatal("initial settings gained missing authority or performed a mutation")
			}
			if err == nil {
				c := domain.ExecutionConfiguration{Harness: domain.OpenCode, NativeModel: profile.Settings.Model, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}
				if observed.ValidateForInput(c, domain.PlanMode) != nil || observed.Effort != nil || observed.ServiceTier != nil {
					t.Fatal("initial settings fabricated native options")
				}
			}
		})
	}
}

func TestExplicitEffortRequiresBothOriginalNativeViews(t *testing.T) {
	for _, drift := range []string{"", "/config", "/provider"} {
		t.Run(drift, func(t *testing.T) {
			p := fixtureAPIProfile()
			p.Settings.Permission = []PermissionRule{}
			p.Settings.Effort = "future-native-effort"
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet || (r.URL.Path != "/config" && r.URL.Path != "/provider") {
					t.Error("settings observation mutated native state")
				}
				value := fixtureEffectiveConfig(p)
				if r.URL.Path == "/provider" {
					value = p.provider()
				}
				if r.URL.Path == drift {
					value["unexpected"] = true
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer server.Close()
			api := &sessionAPI{client: server.Client(), origin: server.URL, cwd: fixtureWorkspacePath(), password: "private-server-secret", gate: make(chan struct{}, 1), apiProfile: p, apiVerified: true, runtimeRoot: t.TempDir(), alive: func() error { return nil }}
			observed, err := api.initialObservedSettings(context.Background())
			if (err == nil) != (drift == "") || api.reconciliationRead {
				t.Fatal("native drift or read scope escaped", err)
			}
			if drift == "" && (reads != 2 || observed.Effort == nil || *observed.Effort != p.Settings.Effort) {
				t.Fatal("requested settings replaced independent native observations")
			}
		})
	}
}
