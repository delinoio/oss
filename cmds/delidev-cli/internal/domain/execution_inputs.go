package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

// Reserve one native tracking slot for the successor input during continuation.
const MaxAcceptedExecutionInputs = 4095

// ExecutionInputBinding is ordered native acceptance evidence. It contains no
// prompt text and never grants authority to claim or send a queued input.
type ExecutionInputBinding struct {
	InputID      ID     `json:"input_id"`
	PromptDigest string `json:"prompt_digest"`
}

func BindExecutionInput(id ID, prompt string) ExecutionInputBinding {
	digest := sha256.Sum256([]byte(prompt))
	return ExecutionInputBinding{InputID: id, PromptDigest: hex.EncodeToString(digest[:])}
}

// CheckedExecutionInputs preserves legacy single-input progress only when the
// field was absent. A supplied list must retain the exact primary input first,
// every distinct later accepted input and canonical SHA-256 digests in order.
func CheckedExecutionInputs(primary ID, digest string, inputs []ExecutionInputBinding) ([]ExecutionInputBinding, error) {
	if inputs == nil {
		inputs = []ExecutionInputBinding{{InputID: primary, PromptDigest: digest}}
	}
	invalid := func() ([]ExecutionInputBinding, error) {
		return nil, Fail(RecoveryRequired, "Accepted execution inputs do not match their original turn.", "Preserve the primary input and every later accepted input identity and digest in delivery order before continuing.")
	}
	if len(inputs) == 0 || len(inputs) > MaxAcceptedExecutionInputs || inputs[0].InputID != primary || inputs[0].PromptDigest != digest {
		return invalid()
	}
	seen := make(map[ID]bool, len(inputs))
	for _, input := range inputs {
		raw, err := hex.DecodeString(input.PromptDigest)
		if input.InputID.Validate() != nil || seen[input.InputID] || err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != input.PromptDigest {
			return invalid()
		}
		seen[input.InputID] = true
	}
	return slices.Clone(inputs), nil
}
