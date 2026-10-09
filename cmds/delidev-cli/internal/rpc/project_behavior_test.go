// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestProjectPlanDecisionKeepsOriginalNativeResourceShape(t *testing.T) {
	original := json.RawMessage(`{"plan_approval_policy":"automatic","approval_response":{"id":"original-response","state":"queued"},"native_request_id":{"kind":"text","text":"original-request"}}`)
	row := store.Record{ID: domain.NewID(), Kind: domain.InteractionKind, Revision: 2, Data: original}
	public := Resource(row)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(public.DocumentJson, &fields); err != nil {
		t.Fatal(err)
	}
	if public.SchemaVersion != 1 || fields["plan_approval_policy"] != nil || fields["approval_response"] == nil || fields["native_request_id"] == nil {
		t.Fatal("native controller wire shape changed")
	}
	// An older Worker has a closed document decoder without the private field.
	var legacy struct {
		ApprovalResponse struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"approval_response"`
		NativeRequestID struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"native_request_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(public.DocumentJson))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil || legacy.ApprovalResponse.ID != "original-response" || legacy.NativeRequestID.Text != "original-request" {
		t.Fatal("strict legacy Worker lost original queued response", err)
	}
	var retained map[string]json.RawMessage
	if err := json.Unmarshal(row.Data, &retained); err != nil || retained["plan_approval_policy"] == nil {
		t.Fatal("durable provenance was altered")
	}
}
