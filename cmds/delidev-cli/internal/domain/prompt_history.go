// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// ProjectPromptHistory retains text only, never execution or skill authority.
type ProjectPromptHistory struct {
	Prompt             string    `json:"prompt"`
	AcceptedAt         time.Time `json:"accepted_at"`
	AcceptanceSequence uint64    `json:"acceptance_sequence"`
}

func (v ProjectPromptHistory) Validate() error {
	if v.AcceptedAt.IsZero() || v.AcceptanceSequence == 0 {
		return Fail(RecoveryRequired, "Prompt history metadata is invalid.", "Preserve the original database.")
	}
	return Text(v.Prompt, "prompt history text", 256<<10, true)
}
