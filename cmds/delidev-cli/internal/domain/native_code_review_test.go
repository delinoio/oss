// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"math"
	"strings"
	"testing"
)

func TestNativeCodeReviewTargetClosure(t *testing.T) {
	for _, target := range []NativeCodeReviewTarget{
		{Kind: ReviewUncommitted, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64)},
		{Kind: ReviewBaseBranch, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64), Reference: "main"},
		{Kind: ReviewCommit, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64), Reference: strings.Repeat("b", 40)},
		{Kind: ReviewCustom, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64), Instructions: "Review the selected changes."},
	} {
		if target.Validate() != nil {
			t.Fatal("original target rejected", target.Kind)
		}
		bad := target
		bad.DiffRevision = "changed"
		if bad.Validate() == nil {
			t.Fatal("unbound target accepted")
		}
		bad = target
		bad.Kind = "feedback"
		if bad.Validate() == nil {
			t.Fatal("ordinary feedback acquired native review")
		}
	}
}
func TestNativeCodeReviewFindingsRejectForeignLocationAndMalformedConfidence(t *testing.T) {
	finding := NativeCodeReviewFinding{Title: "Retain original identity", Body: "The observation uses the wrong source.", Confidence: 0.8, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2}
	if finding.Validate() != nil {
		t.Fatal("bounded finding rejected")
	}
	for _, mutate := range []func(*NativeCodeReviewFinding){
		func(f *NativeCodeReviewFinding) { f.Path = "../foreign.go" }, func(f *NativeCodeReviewFinding) { f.Path = "/private/raw/path.go" },
		func(f *NativeCodeReviewFinding) { f.Confidence = math.NaN() }, func(f *NativeCodeReviewFinding) { f.Confidence = math.Inf(1) },
		func(f *NativeCodeReviewFinding) { f.Priority = 4 }, func(f *NativeCodeReviewFinding) { f.StartLine = 0 },
	} {
		bad := finding
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("foreign or malformed finding accepted")
		}
	}
}
