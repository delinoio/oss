// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// ManagedExecutionConfig accepts only the protected Take delivery. API tokens,
// caller-selected issuers and imported host authentication have no fields here.
type ManagedExecutionConfig = managedProfileConfig

// OwnedManaged keeps the account bundle boundary separate from API authority.
// Its original controllers do not independently grant repository/Resume support.
type OwnedManaged struct{ *OwnedAPI }

func OpenOwnedManagedWithPlanning(ctx context.Context, config ManagedExecutionConfig, record PlanningRecorders, beforeAuthentication func(string) error) (*OwnedManaged, error) {
	if record.Creation == nil || record.Mode == nil || record.Input == nil || record.Closure == nil || record.Stop == nil || record.File == nil || record.Question == nil || record.Plan == nil || beforeAuthentication == nil {
		return nil, subscription.InvalidGrok()
	}
	connection, err := openManaged(ctx, managedOpeningConfig{managedProfileConfig: config, beforeAuthentication: beforeAuthentication})
	if err != nil {
		return nil, err
	}
	return &OwnedManaged{&OwnedAPI{connection: connection, creation: record.Creation, mode: record.Mode, input: record.Input, fileReply: record.File, questionReply: record.Question, planReply: record.Plan, closure: record.Closure, stop: record.Stop}}, nil
}

// CapturedBundle returns the original once-only pre-close observation. Returning
// it does not prove process/file cleanup or authorize protected Finish delivery.
func (a *OwnedManaged) CapturedBundle() ([]byte, error) {
	if a == nil || a.OwnedAPI == nil || a.connection == nil || a.connection.managedBundle == nil {
		return nil, subscription.InvalidGrok()
	}
	return a.connection.managedBundle.take()
}
