// SPDX-License-Identifier: Apache-2.0
package domain

// This copies the selected model's metadata into the immutable native profile.
// It remains declared/known metadata, not measured upstream context capacity.
// Omitted legacy snapshots preserve the native unknown-limit representation.
type OpenCodeModelContext struct {
	Tokens uint64         `json:"tokens"`
	Source EvidenceSource `json:"source"`
}

func (c OpenCodeModelContext) Validate() error {
	if c.Tokens < 1024 || c.Tokens > 1_000_000_000 || c.Source != Known && c.Source != UserDeclared {
		return Fail(Unsupported, "The selected OpenCode context metadata is unsupported.", "Keep an explicit known or declared context limit from 1,024 to 1,000,000,000 tokens; unavailable limits stay omitted.")
	}
	return nil
}
