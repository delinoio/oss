package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestGrokInteractionPreservesExactRequestRepresentation(t *testing.T) {
	raw, err := os.ReadFile("../harness/grok/testdata/file-tool-write.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Method GrokToolMethod  `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("original request fixture")
	}
	var payload GrokToolPayload
	var proposal string
	for _, row := range rows {
		if row.Method == GrokFilePermissionMethod {
			if Decode(row.Params, &payload) != nil {
				t.Fatal("original request payload")
			}
			proposal = string(row.Params)
			break
		}
	}
	if proposal == "" || payload.Tool == nil || payload.Tool.ID == nil {
		t.Fatal("original Write proposal absent")
	}
	left, right := int64(7), int64(7)
	legacy := InteractionRequestID{Kind: InteractionNumberID, Number: &left}
	decimal := InteractionRequestID{Kind: InteractionDecimalID, Decimal: "7"}
	for _, tc := range []struct {
		name     string
		outer    InteractionRequestID
		retained InteractionRequestID
		valid    bool
	}{
		{"text", InteractionRequestID{Kind: InteractionTextID, Text: "original"}, InteractionRequestID{Kind: InteractionTextID, Text: "original"}, true},
		{"legacy-number-values", legacy, InteractionRequestID{Kind: InteractionNumberID, Number: &right}, true},
		{"decimal", decimal, decimal, true},
		{"negative-zero", InteractionRequestID{Kind: InteractionDecimalID, Decimal: "-0"}, InteractionRequestID{Kind: InteractionDecimalID, Decimal: "-0"}, true},
		{"outside-int64", InteractionRequestID{Kind: InteractionDecimalID, Decimal: "9223372036854775808"}, InteractionRequestID{Kind: InteractionDecimalID, Decimal: "9223372036854775808"}, true},
		{"decimal-to-legacy", legacy, decimal, false},
		{"legacy-to-decimal", decimal, legacy, false},
		{"changed-spelling", InteractionRequestID{Kind: InteractionDecimalID, Decimal: "0"}, InteractionRequestID{Kind: InteractionDecimalID, Decimal: "-0"}, false},
		{"changed-namespace", InteractionRequestID{Kind: InteractionTextID, Text: "7"}, decimal, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := tc.outer.Key()
			if err != nil {
				t.Fatal(err)
			}
			requestSum := sha256.Sum256([]byte(key))
			proposalSum := sha256.Sum256([]byte(proposal))
			request := GrokInteractionRequest{Version: GrokProtocolVersion, ObservationID: NewID(), Event: GrokToolEvent{Method: GrokFilePermissionMethod, ArrivalID: NewID(), RequestID: &tc.retained, Payload: payload, ProposalJSON: proposal}, RequestDigest: hex.EncodeToString(requestSum[:]), ProposalDigest: hex.EncodeToString(proposalSum[:])}
			if valid := request.Validate(NativeApprovalInteraction, tc.outer, *payload.Tool.ID) == nil; valid != tc.valid {
				t.Fatalf("exact representation validation = %v, want %v", valid, tc.valid)
			}
		})
	}
	legacyKey, _ := legacy.Key()
	decimalKey, _ := decimal.Key()
	if legacyKey != decimalKey {
		t.Fatal("duplicate-detection namespace compatibility changed")
	}
}
