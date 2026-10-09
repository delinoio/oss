// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// PutGrokAccounting is called only from the verified completion transaction.
// Native terminal publication is insufficient without independently confirmed
// workspace cleanup. Legacy terminals without original user history stay absent.
func (t *Tx) PutGrokAccounting(jobID, projectID domain.ID, input domain.ExecutionJobInput, p domain.ExecutionProgress, completion domain.ExecutionCompletion) error {
	if p.GrokTerminal == nil || p.GrokTerminal.User == nil {
		return nil
	}
	record := domain.GrokAccountingRecord{Kind: domain.GrokClosedInput, SourceReceipt: t.requestID, SourceUsageID: p.LatestUsageID, JobID: jobID, SessionID: input.SessionID, ProjectID: projectID, ExecutionID: input.ExecutionID, InputID: input.InputID, InputRequestID: input.TurnRequestID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProviderID: input.Configuration.ProviderID, ModelID: input.Configuration.ModelID, Version: input.Installation.Version, Terminal: *p.GrokTerminal, Completion: completion}
	if record.Validate() != nil || input.Validate() != nil || !p.CleanupVerified || p.GrokStop != nil || p.InputID != input.InputID || p.ExecutionID != input.ExecutionID || p.LastSequence != completion.LastSequence || p.NativeTurnID != string(completion.NativeTurnID) || p.NativeThreadID != string(completion.NativeThreadID) || record.Terminal.Model != input.Configuration.NativeModel || record.Terminal.User.InputDigest != domain.GrokUserInputDigest(input.Input.Prompt) {
		return corrupt()
	}
	source, err := t.Get(domain.UsageKind, record.SourceUsageID)
	if err != nil {
		return err
	}
	usage, err := Decode[domain.GrokUsageRecord](source)
	if err != nil || source.SessionID != input.SessionID || usage.ExecutionID != input.ExecutionID || usage.ThreadID != p.NativeThreadID || usage.TurnID != p.NativeTurnID || usage.Harness != domain.GrokBuild || usage.Version != record.Version || usage.AccountID != record.AccountID || usage.ConnectionID != record.ConnectionID || usage.ProviderID != record.ProviderID || usage.ModelID != record.ModelID || usage.Sequence >= completion.LastSequence || usage.Usage.Ordinal != 1 || usage.Usage.Counts != record.Terminal.Counts {
		return corrupt()
	}
	body, err := json.Marshal(record)
	if err != nil || len(body) > 16<<10 {
		return corrupt()
	}
	var retained []byte
	err = t.tx.QueryRowContext(t.ctx, "SELECT body FROM native_accounting WHERE id=? OR (kind=? AND execution_id=? AND input_id=?)", record.SourceReceipt, record.Kind, record.ExecutionID, record.InputID).Scan(&retained)
	if err == nil {
		if !bytes.Equal(body, retained) {
			return corrupt()
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return storageError(err)
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO native_accounting(id,kind,session_id,project_id,execution_id,input_id,account_id,provider_id,model_key,body,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", record.SourceReceipt, record.Kind, record.SessionID, record.ProjectID, record.ExecutionID, record.InputID, record.AccountID, record.ProviderID, record.ModelID, body, t.now.UnixMilli())
	// Deliberately no price snapshot or session_estimates write: the pinned Grok
	// category inclusivity is not proved. Remove this exclusion only with a separately
	// verified native pricing profile, never by reinterpreting response dimensions.
	return storageError(err)
}
