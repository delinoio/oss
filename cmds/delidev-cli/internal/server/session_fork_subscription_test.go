// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestSessionForkRefusesManagedAuthenticationAssignmentBeforeNativeWork(t *testing.T) {
	f, _, accepted := acceptedForkFixture(t)
	_, input := forkClaimFixture(t, f, accepted.Job.Id)
	if err := input.Validate(); err != nil {
		t.Fatal("ordinary API fork lost eligibility", err)
	}
	input.SourceAssignment.Configuration.Subscription = true
	digest, err := input.SourceAssignment.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	input.SourceAssignment.ConfigurationDigest = digest
	input.Snapshot.Configuration = input.SourceAssignment.Configuration
	input.Snapshot.ConfigurationDigest = digest
	// The source assignment itself remains valid. Fork's API-only native
	// coordinator cannot acquire managed credentials from this configuration.
	if err := input.SourceAssignment.Validate(); err != nil {
		t.Fatal("invalid managed source assignment", err)
	}
	if err := input.Validate(); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("API-only native fork admitted managed authentication", err)
	}
}
