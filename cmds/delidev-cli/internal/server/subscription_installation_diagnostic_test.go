// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubscriptionInstallationReportsVerificationInsteadOfVersionRequirement(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.Machine)
		code   domain.Code
	}{
		{"verified newer", func(m *domain.Machine) {}, ""},
		{"protocol unverified", func(m *domain.Machine) { m.Installations[0].ProtocolVerified = false }, domain.Unsupported},
		{"missing installation", func(m *domain.Machine) { m.Installations = nil }, domain.Unsupported},
		{"missing path", func(m *domain.Machine) { m.Installations[0].ResolvedPath = "" }, domain.Unsupported},
		{"missing observation", func(m *domain.Machine) { m.Installations[0].ObservedAt = nil }, domain.Unsupported},
		{"future observation", func(m *domain.Machine) { at := time.Now().UTC().Add(time.Hour); m.Installations[0].ObservedAt = &at }, domain.Unsupported},
		{"installation problem", func(m *domain.Machine) {
			m.Installations[0].Problem = domain.Fail(domain.Unavailable, "safe fixture", "")
		}, domain.Unsupported},
		{"missing capability", func(m *domain.Machine) { m.WorkerCapabilities = nil }, domain.Unsupported},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			f := newSubscriptionFixture(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.installation.diagnostic", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.MachineKind, f.input.MachineID)
				if err != nil {
					return nil, err
				}
				m, err := store.Decode[domain.Machine](r)
				if err != nil {
					return nil, err
				}
				m.Installations[0].Version = "0.159.2"
				scenario.change(&m)
				if _, err := tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m); err != nil {
					return nil, err
				}
				installation, admissionErr := subscriptionInstallation(tx, r.ID)
				if scenario.code == "" {
					if admissionErr != nil || installation.Version != "0.159.2" {
						t.Fatalf("newer verified installation rejected: %v", admissionErr)
					}
					return nil, nil
				}
				if admissionErr == nil {
					t.Fatal("incomplete installation accepted")
				}
				problem := domain.SafeError(admissionErr)
				if problem.Code != scenario.code || strings.Contains(problem.Message+problem.Guidance, "0.151.0") {
					t.Fatalf("inaccurate diagnostic: %+v", problem)
				}
				if scenario.name != "missing capability" && (problem.Message != "The selected Runner Device has no verified Codex installation." || !strings.Contains(problem.Guidance, "protocol verification")) {
					t.Fatalf("verification guidance missing: %+v", problem)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
