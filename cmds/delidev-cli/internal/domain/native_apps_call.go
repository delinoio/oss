// SPDX-License-Identifier: Apache-2.0
package domain

// NativeAppCallProof is protected original Worker provenance. It is retained
// with the native question and never projected as product installation or
// account authority. The first response claim revalidates this exact frozen
// selection atomically with revocation before admitting the original release.
type NativeAppCallProof struct {
	Scope        NativeAppScope            `json:"scope"`
	Selection    SessionNativeAppSelection `json:"selection"`
	Inventory    NativeAppInventory        `json:"inventory"`
	NativeItemID string                    `json:"native_item_id"`
	AppID        string                    `json:"app_id"`
}

func (p NativeAppCallProof) Validate() error {
	if p.Scope != p.Selection.Scope || p.Scope != p.Inventory.Scope || Text(p.NativeItemID, "original native App call", 1024, true) != nil {
		return NativeAppsUnavailable()
	}
	return AdmitNativeAppCall(p.Selection, p.Selection, p.Inventory, p.AppID)
}

type NativeAppsApprovalChoice string

const (
	NativeAppsAllow  NativeAppsApprovalChoice = "Allow"
	NativeAppsCancel NativeAppsApprovalChoice = "Cancel"
)

func ValidateNativeAppsQuestion(proof NativeAppCallProof, q *QuestionRequest) error {
	if proof.Validate() != nil || q == nil || q.Validate() != nil || !q.Blocking || q.AutoResolutionMS != nil || len(q.Questions) != 1 {
		return NativeAppsUnavailable()
	}
	question := q.Questions[0]
	if question.ID != "mcp_tool_call_approval_"+proof.NativeItemID || question.Header != "Approve app tool call?" || question.Other || question.Secret || len(question.Options) != 2 || question.Options[0].Label != string(NativeAppsAllow) || question.Options[1].Label != string(NativeAppsCancel) {
		return NativeAppsUnavailable()
	}
	return nil
}

// Cancel closes the original native prompt without granting a connector effect.
// Only the pinned literal Allow choice can enter the first release-admission
// transaction. Free-form, remembered, missing or foreign answers grant nothing.
func NativeAppsResponseAdmitsEffect(value ExecutionInteraction) (bool, error) {
	if value.NativeApps == nil {
		return false, nil
	}
	if value.Type != UserQuestionInteraction || value.Approval != nil || value.Grok != nil || value.Claude != nil || value.OpenCode != nil || value.NativeApps.NativeItemID != value.NativeItemID || ValidateNativeAppsQuestion(*value.NativeApps, value.Questions) != nil || value.Response == nil || value.Response.Input.ValidateInteraction(value) != nil {
		return false, NativeAppsUnavailable()
	}
	answers := value.Response.Input.Answers
	id := value.Questions.Questions[0].ID
	if len(answers) != 1 || len(answers[id]) != 1 {
		return false, NativeAppsUnavailable()
	}
	switch NativeAppsApprovalChoice(answers[id][0]) {
	case NativeAppsAllow:
		return true, nil
	case NativeAppsCancel:
		return false, nil
	default:
		return false, NativeAppsUnavailable()
	}
}
