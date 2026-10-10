// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func validatePublishedNativeAppCall(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	proof := event.Interaction.NativeApps
	if proof == nil || input.NativeApps == nil || proof.Validate() != nil || input.Configuration.Harness != domain.Codex || input.Fork != nil {
		return domain.NativeAppsUnavailable()
	}
	scope := domain.NativeAppsAssignmentScope(input)
	if scope != proof.Scope {
		return domain.NativeAppsUnavailable()
	}
	original, _ := json.Marshal(input.NativeApps)
	reported, _ := json.Marshal(proof.Selection)
	if !bytes.Equal(original, reported) {
		return domain.NativeAppsUnavailable()
	}
	tool, err := tx.CodexAppsTool(session.ID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, proof.NativeItemID)
	if err != nil {
		return err
	}
	if tool.Role != domain.ToolMessage || tool.State != domain.MessageStreaming || tool.Tool == nil || tool.Tool.Completed != nil || tool.Tool.Started.Kind != domain.NativeAppsTool || tool.Tool.Started.Apps == nil || tool.Tool.Started.Apps.AppID != proof.AppID {
		return domain.NativeAppsUnavailable()
	}
	return nil
}

// A retained terminal result cannot manufacture a one-call release. Revocation
// after the durable admission does not retroactively change that original fact.
func validateCompletedNativeAppCall(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent) error {
	ids, err := tx.ExecutionItemInteractions(input.ExecutionID, event.NativeThreadID, event.NativeTurnID, event.Tool.NativeID)
	if err != nil {
		return err
	}
	if len(ids) == 0 && event.Tool.Snapshot.Status == domain.ToolFailed && event.Tool.Snapshot.Apps != nil && event.Tool.Snapshot.Apps.Result == nil && event.Tool.Snapshot.Apps.ErrorPresent {
		// A closed original native failure without a result grants no release.
		return nil
	}
	if len(ids) != 1 {
		return domain.NativeAppsUnavailable()
	}
	row, err := tx.Get(domain.InteractionKind, ids[0])
	if err != nil {
		return err
	}
	value, err := store.Decode[domain.ExecutionInteraction](row)
	if err != nil {
		return err
	}
	if row.SessionID != input.SessionID || value.NativeApps == nil || value.NativeApps.AppID != event.Tool.Snapshot.Apps.AppID || value.NativeItemID != event.Tool.NativeID || value.Response == nil || value.Response.Claim == nil || value.Response.State == domain.QuestionResponseQueued || value.Response.State == domain.QuestionResponseCanceled {
		return domain.NativeAppsUnavailable()
	}
	allow, err := domain.NativeAppsResponseAdmitsEffect(value)
	if err != nil || !allow && event.Tool.Snapshot.Status != domain.ToolFailed {
		return domain.NativeAppsUnavailable()
	}
	return nil
}
