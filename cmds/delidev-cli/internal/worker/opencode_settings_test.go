package worker

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func TestOpenCodeRequestedSettingsPreserveImmutableSelections(t *testing.T) {
	f := newCheckpointFixture(t)
	c := f.input.Configuration
	c.Harness, c.Effort, c.Options = domain.OpenCode, "", domain.AgentOptions{Permission: domain.PermissionDefault}
	before, err := c.Digest()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		settings, err := openCodeExecutionSettings(c, mode, "Original session title")
		if err != nil {
			t.Fatal(err)
		}
		agent := opencode.BuildAgent
		if mode == domain.PlanMode {
			agent = opencode.PlanAgent
		}
		if settings.Session.Agent != agent || settings.Session.Model != c.NativeModel || settings.Session.Provider != openCodeAPIProvider || settings.Session.Title != "Original session title" || settings.Session.Permission == nil || len(settings.Session.Permission) != 0 || settings.Instructions != c.Instructions || settings.Rejection != opencode.StopOnInteractionRejection {
			t.Fatal("requested native settings dropped original instructions, mode or defaults")
		}
		after, _ := c.Digest()
		raw, _ := json.Marshal(settings)
		if after != before || string(raw) != "{}" {
			t.Fatal("native mapping rewrote the snapshot or disclosed private contents")
		}
	}
	for _, change := range []func(*domain.ExecutionConfiguration){
		func(c *domain.ExecutionConfiguration) { c.Harness = domain.Codex },
		func(c *domain.ExecutionConfiguration) { c.Effort = "invalid\x00effort" },
		func(c *domain.ExecutionConfiguration) { c.Options.Permission = domain.PermissionReadOnly },
		func(c *domain.ExecutionConfiguration) { c.Options.ApprovalPolicy = "never" },
		func(c *domain.ExecutionConfiguration) { c.Options.ClaudePermission = domain.ClaudePermissionPlan },
		func(c *domain.ExecutionConfiguration) { c.Options.SubagentModel = "another-model" },
		func(c *domain.ExecutionConfiguration) { c.Options.SubagentEffort = "high" },
		func(c *domain.ExecutionConfiguration) { c.Options.MaxConcurrency = 1 },
		func(c *domain.ExecutionConfiguration) { c.Options.ApprovalReviewModel = "another-model" },
		func(c *domain.ExecutionConfiguration) { c.Options.ServiceTier = "fast" },
		func(c *domain.ExecutionConfiguration) { c.Instructions += "unretained instruction" },
	} {
		other := c
		change(&other)
		if _, err := openCodeExecutionSettings(other, domain.PlanMode, "Original title"); err == nil {
			t.Fatal("explicit incompatible selection was silently omitted")
		}
	}
	if _, err := openCodeExecutionSettings(c, domain.PlanMode, ""); err == nil {
		t.Fatal("missing native title accepted")
	}
	if _, err := openCodeExecutionSettings(c, "unknown", "Original title"); err == nil {
		t.Fatal("unknown input mode accepted")
	}
}
