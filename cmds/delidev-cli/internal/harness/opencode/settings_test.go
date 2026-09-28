package opencode

import (
	"context"
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
