// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

// Synthetic assignment/checkpoint fixtures verify durable handling rules.
// They do not claim native or real-account push acceptance.
func TestPRFixOnlyVerifiedOriginalPushHandlesEvidence(t *testing.T) {
	for _, scenario := range []string{"verified", "unchanged", "missing", "uncertain", "foreign-attempt", "foreign-selection", "failed-native", "dismissed"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			f := newRemediationStoreFixture(t, s)
			f.observation.Items[0].HeadRepository = &domain.PRHeadRepositoryObservation{State: domain.PRHeadRepositoryAvailable, Repository: &f.observation.Repository}
			target, err := domain.NewPRGitTarget(f.observation)
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.reserve(t, domain.PRRemediationManual)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.fix-target", nil, func(tx *Tx) (any, error) {
				a, err = tx.BindPRFixTarget(a.ID, a.Revision, f.project, target)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			a = f.bind(t, a)
			a, _, err = f.start(t, a, domain.NewID(), f.observation)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "dismissed" {
				_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.fix-dismiss", nil, func(tx *Tx) (any, error) {
					row, p, err := tx.GetPRProblem(f.problems[0].ID)
					if err != nil {
						return nil, err
					}
					return tx.DismissPRProblem(row.ID, row.Revision, p.ContentVersion)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			finished := f.finish(t, a, true, func(j *domain.Job) {
				var input domain.ExecutionJobInput
				var done domain.ExecutionCompletion
				domain.Decode(j.Input, &input)
				domain.Decode(j.Output, &done)
				selection := domain.PRFixExecution{AttemptID: a.ID, Target: target, Strategy: f.policy.ConflictStrategy, Conflict: true}
				input.Remediation = &selection
				proof := domain.PRPushProof{Version: 1, AttemptID: a.ID, ExecutionID: done.ExecutionID, SelectionDigest: selection.Digest(), State: domain.PRPushVerified, PreviousHead: target.HeadSHA, ResultHead: strings.Repeat("c", 40), ObservedAt: *j.FinishedAt}
				switch scenario {
				case "unchanged":
					proof.State, proof.ResultHead = domain.PRPushUnchanged, target.HeadSHA
				case "uncertain":
					proof.State, proof.ResultHead = domain.PRPushUncertain, ""
				case "foreign-attempt":
					proof.AttemptID = domain.NewID()
				case "foreign-selection":
					proof.SelectionDigest = strings.Repeat("d", 64)
				case "failed-native":
					done.Outcome = domain.ExecutionFailed
					j.State = domain.JobFailed
					j.Problem = domain.Fail(domain.Unavailable, "Fixture native failure.", "")
				}
				if scenario != "missing" {
					done.PRPush = &proof
				}
				j.Input, _ = json.Marshal(input)
				j.Output, _ = json.Marshal(done)
			})
			v, _ := Decode[domain.PRRemediationAttempt](finished)
			rows := readProblemFixture(t, s, f.set.ID)
			p, _ := Decode[domain.PRProblem](rows[0])
			verifications := 0
			if err := s.Read(notificationOwner(), func(tx *Tx) error {
				all, err := tx.List(Filter{Kind: domain.ProblemKind, Limit: 100})
				if err != nil {
					return err
				}
				for _, row := range all {
					var value domain.PRHandlingVerification
					if json.Unmarshal(row.Data, &value) == nil && value.Type == domain.PRHandlingVerificationRecord {
						if value.Validate() != nil || value.SetID != f.set.ID || len(value.Problems) != 1 || value.Problems[0] != f.problems[0] {
							t.Fatal("foreign Activity proof")
						}
						verifications++
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if scenario == "verified" && verifications != 1 || scenario != "verified" && verifications != 0 {
				t.Fatal("outcome fabricated or omitted Activity proof", scenario, verifications)
			}
			if scenario == "verified" {
				if p.State != domain.PRProblemHandled || p.Handling == nil || p.Handling.AttemptID != a.ID || v.State != domain.PRRemediationFinished {
					t.Fatal("verified push lost handling", v.State, p.State)
				}
			} else {
				if p.State == domain.PRProblemHandled || p.Handling != nil {
					t.Fatal("unverified push handled", scenario)
				}
				if scenario != "unchanged" && scenario != "dismissed" && v.State != domain.PRRemediationUncertain {
					t.Fatal("lost original uncertain owner", scenario, v.State)
				}
			}
		})
	}
}
