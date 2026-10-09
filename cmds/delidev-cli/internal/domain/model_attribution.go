// SPDX-License-Identifier: Apache-2.0
package domain

type ModelAttribution string

const (
	BuiltinReviewerAttribution ModelAttribution = "codex-reviewer-gpt-5.6-luna"
	UnknownReviewAttribution   ModelAttribution = "unknown-auto-review-model"
)

func (a ModelAttribution) Valid() bool {
	return a == "" || a == BuiltinReviewerAttribution || a == UnknownReviewAttribution
}
func (a ModelAttribution) Label() string {
	if a == BuiltinReviewerAttribution {
		return CodexReviewerNativeModel
	}
	return ""
}
