// SPDX-License-Identifier: Apache-2.0
package domain

// ApprovalsReviewer selects native review without granting sandbox permissions.
type ApprovalsReviewer string

const (
	CodexReviewerUser        ApprovalsReviewer = "user"
	CodexReviewerAuto        ApprovalsReviewer = "auto_review"
	CodexReviewerNativeModel                   = "gpt-5.6-luna"
	CodexApprovalReviewV1    WorkerCapability  = "codex-approval-review-v1"
)

func (r ApprovalsReviewer) Effective() ApprovalsReviewer {
	if r == "" {
		return CodexReviewerUser
	}
	return r
}
func (r ApprovalsReviewer) Valid() bool {
	return r == "" || r == CodexReviewerUser || r == CodexReviewerAuto
}
func (o AgentOptions) ValidateReviewer() error {
	if !o.ApprovalsReviewer.Valid() || o.ApprovalsReviewer == CodexReviewerAuto && o.ApprovalPolicy != "on-request" {
		return Fail(Unsupported, "The native approval reviewer selection is incompatible.", "Select User, or AI auto-review with on-request approvals. Preserve the selected sandbox.")
	}
	return nil
}
