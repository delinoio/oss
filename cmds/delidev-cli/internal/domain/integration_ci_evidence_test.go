package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCIResultVersionRetainsLifecycleAndOutputWithoutBorrowingAggregateAttempts(t *testing.T) {
	_, fixture := ciFixture()
	v := fixture.Head.Contexts[0]
	version := v.Version()
	if len(version) != 64 {
		t.Fatal("missing original version")
	}
	v.Required = false
	v.Application.Slug = "renamed-app"
	v.Evidence.Workflow.ObservedAttempt = "2"
	v.Evidence.Workflow.UpdatedAt = v.Evidence.Workflow.UpdatedAt.Add(time.Minute)
	if v.Version() != version {
		t.Fatal("applicability or aggregate workflow update became a new result")
	}
	completed := v.Evidence.CompletedAt.Add(time.Minute)
	v.Evidence.CompletedAt = &completed
	if v.Version() == version {
		t.Fatal("new lifecycle collapsed")
	}
	version = v.Version()
	output := "Original result output <script>inert</script>"
	v.Evidence.Text = &output
	if v.Version() == version {
		t.Fatal("edited original output collapsed")
	}
	version = v.Version()
	v.NodeID = "CHECK_rerun"
	if v.Version() == version {
		t.Fatal("distinct result identity collapsed")
	}
	v.Evidence = nil
	if v.Version() != "" {
		t.Fatal("missing evidence manufactured a version")
	}
}

func TestCIEvidenceRejectsMissingMixedMalformedAndOversizedProof(t *testing.T) {
	for _, mutate := range []func(*CIContext){
		func(v *CIContext) { v.Evidence = nil },
		func(v *CIContext) { v.Evidence.SuiteNodeID = "" },
		func(v *CIContext) { v.Evidence.CreatedAt = v.Evidence.StartedAt },
		func(v *CIContext) { v.Evidence.Workflow.ObservedAttempt = "0" },
		func(v *CIContext) { v.Evidence.Workflow.RunNumber = "2147483648" },
		func(v *CIContext) { v.Evidence.Workflow.UpdatedAt = time.Time{} },
		func(v *CIContext) { at := v.Evidence.StartedAt.Add(-time.Minute); v.Evidence.CompletedAt = &at },
		func(v *CIContext) { output := strings.Repeat("x", (64<<10)+1); v.Evidence.Text = &output },
	} {
		_, fixture := ciFixture()
		v := fixture.Head.Contexts[0]
		mutate(&v)
		if v.Validate(v.CommitSHA) == nil || v.Version() != "" {
			t.Fatal("invalid lifecycle or output accepted")
		}
	}
	item, fixture := ciFixture()
	output := strings.Repeat("x", 64<<10)
	for i := 0; i < 3; i++ {
		v := fixture.Head.Contexts[0]
		v.NodeID = "CHECK_" + strings.Repeat("x", i+1)
		copy := *v.Evidence
		copy.Title, copy.Summary, copy.Text = &output, &output, &output
		v.Evidence = &copy
		fixture.Head.Contexts = append(fixture.Head.Contexts, v)
	}
	fixture.Head.TotalCount = "4"
	fixture.Result = fixture.Evaluate(item)
	if fixture.Validate(item) == nil {
		t.Fatal("aggregate original output exceeded complete publication bound")
	}
	created := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	status := CIContext{Kind: CICommitStatus, NodeID: "STATUS_1", Name: "context", CommitSHA: item.HeadSHA, NativeStatus: "FAILURE", Evidence: &CIContextEvidence{CreatedAt: &created, UpdatedAt: &created}}
	if status.Validate(item.HeadSHA) != nil || status.Version() == "" {
		t.Fatal("original commit status rejected")
	}
	old := status.Version()
	updated := created.Add(time.Second)
	status.Evidence.UpdatedAt = &updated
	if status.Version() == old {
		t.Fatal("status update collapsed")
	}
	status.Evidence.UpdatedAt = nil
	if status.Validate(item.HeadSHA) == nil {
		t.Fatal("status without original update accepted")
	}
}
