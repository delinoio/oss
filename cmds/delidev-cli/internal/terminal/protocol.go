// SPDX-License-Identifier: Apache-2.0
// Package terminal defines the bounded Worker terminal protocol. Native shell
// ownership and product mutation authority remain separate from output bytes.
package terminal

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// MaxResultBytes covers two 4,096-byte paths even when JSON escapes each byte
// to six bytes, with remaining room for bounded native state and safe errors.
const MaxResultBytes = 64 << 10

type Assignment struct {
	ID          domain.ID                 `json:"id"`
	SessionID   domain.ID                 `json:"session_id"`
	Terminal    domain.Terminal           `json:"terminal"`
	Operation   domain.TerminalOperation  `json:"operation"`
	Preparation *workspace.PrepareRequest `json:"preparation,omitempty"`
	Manifest    *workspace.Manifest       `json:"manifest,omitempty"`
}

type Result struct {
	State           domain.TerminalState `json:"state"`
	Shell           string               `json:"shell,omitempty"`
	Cwd             string               `json:"cwd,omitempty"`
	Rows            uint16               `json:"rows"`
	Columns         uint16               `json:"columns"`
	OutputLost      bool                 `json:"output_lost,omitempty"`
	CleanupVerified bool                 `json:"cleanup_verified"`
	ExitCode        *int                 `json:"exit_code,omitempty"`
	Problem         *domain.Error        `json:"problem,omitempty"`
}

func (r Result) Validate() error {
	if r.Rows == 0 || r.Columns == 0 || r.Rows > 500 || r.Columns > 1000 || domain.Text(r.Shell, "terminal shell", 4096, false) != nil || domain.Text(r.Cwd, "terminal directory", 4096, false) != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid terminal result.", "Preserve the original terminal assignment.")
	}
	switch r.State {
	case domain.TerminalRunning:
		if !r.CleanupVerified && r.ExitCode == nil && r.Shell != "" && r.Cwd != "" {
			return nil
		}
	case domain.TerminalClosed, domain.TerminalExited:
		if r.CleanupVerified {
			return nil
		}
	case domain.TerminalUncertain:
		if !r.CleanupVerified && r.Problem != nil {
			return nil
		}
	}
	return domain.Fail(domain.InvalidArgument, "Terminal state and cleanup proof disagree.", "Reconcile original native ownership.")
}
