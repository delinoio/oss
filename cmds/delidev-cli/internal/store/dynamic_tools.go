// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// The existing execution index bounds dynamic ownership and settlement reads.
// No new table or migration is needed for this additive message union.
func (t *Tx) DynamicToolsSettled(execution domain.ID) (bool, error) {
	if execution.Validate() != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid execution identity.", "Inspect the original execution.")
	}
	var unsettled bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.execution_id=? AND json_type(e.body,'$.dynamic_tool')='object' AND (COALESCE(json_extract(e.body,'$.dynamic_tool.snapshot.status'),'inProgress')='inProgress' OR (json_type(e.body,'$.dynamic_tool.request')='object' AND (COALESCE(json_extract(e.body,'$.dynamic_tool.resolved'),0)!=1 OR COALESCE(json_extract(e.body,'$.dynamic_tool.reply.delivery'),'')!='transmitted'))))`, execution).Scan(&unsettled)
	return !unsettled, storageError(err)
}
func (t *Tx) DynamicRequestUnique(session, execution, message domain.ID, request domain.DynamicToolRequest) (bool, error) {
	if session.Validate() != nil || execution.Validate() != nil || message.Validate() != nil || request.Validate() != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid dynamic request identity.", "Retain its original request ownership.")
	}
	var duplicate bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM execution_messages m JOIN entities e ON e.id=m.message_id WHERE m.session_id=? AND m.execution_id=? AND m.message_id!=? AND json_type(e.body,'$.dynamic_tool.request')='object' AND (json_extract(e.body,'$.dynamic_tool.request.token')=? OR (json_extract(e.body,'$.dynamic_tool.request.id_kind')=? AND json_extract(e.body,'$.dynamic_tool.request.id_value')=?)))`, session, execution, message, request.Token, request.IDKind, request.IDValue).Scan(&duplicate)
	if err != nil || duplicate {
		return false, storageError(err)
	}
	// Human interaction controllers cannot borrow this original wire identity.
	err = t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.execution_id')=? AND (id=? OR (json_extract(body,'$.native_request_id.kind')=? AND COALESCE(CAST(json_extract(body,'$.native_request_id.number') AS TEXT),json_extract(body,'$.native_request_id.text'))=?)))`, domain.InteractionKind, session, execution, request.Token, request.IDKind, request.IDValue).Scan(&duplicate)
	return !duplicate, storageError(err)
}
