package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"google.golang.org/protobuf/proto"
)

func originalGrokCLIRequest(t *testing.T, fixture string, method domain.GrokToolMethod) domain.ExecutionInteraction {
	t.Helper()
	raw, err := os.ReadFile("../harness/grok/testdata/" + fixture + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Method domain.GrokToolMethod `json:"method"`
		Params json.RawMessage       `json:"params"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("fixture")
	}
	for _, row := range rows {
		if row.Method != method {
			continue
		}
		var p domain.GrokToolPayload
		if domain.Decode(row.Params, &p) != nil {
			t.Fatal("original")
		}
		kind, tool := domain.UserQuestionInteraction, p.ToolID
		if method == domain.GrokFilePermissionMethod {
			kind, tool = domain.NativeApprovalInteraction, p.Tool.ID
		}
		native := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: "original-native-cli"}
		sum := sha256.Sum256(row.Params)
		return domain.ExecutionInteraction{ExecutionID: domain.NewID(), NativeThreadID: string(p.Session), NativeTurnID: "e5833c4a-d764-4428-8bd8-6c2968a34b1b", NativeItemID: *tool, NativeRequestID: native, Type: kind, Closure: domain.InteractionOpen, Grok: &domain.GrokInteractionRequest{Version: domain.GrokProtocolVersion, ObservationID: domain.NewID(), Event: domain.GrokToolEvent{Method: method, RequestID: &native, ArrivalID: domain.NewID(), Payload: p}, RequestDigest: hex.EncodeToString(sum[:]), ProposalDigest: hex.EncodeToString(sum[:])}}
	}
	t.Fatal("request absent")
	return domain.ExecutionInteraction{}
}

func TestCLIGrokOriginalQuestionAndWriteResponseReceiptParity(t *testing.T) {
	for _, question := range []bool{true, false} {
		t.Run(map[bool]string{true: "question", false: "write"}[question], func(t *testing.T) {
			var root, id string
			request := string(domain.NewID())
			var requests func() int
			var invoke func(string) (int, map[string]any)
			var closeOriginal func()
			var check func()
			if question {
				f := newQuestionCLIFixture(t)
				root, id = f.root, f.resource.Id
				document := originalGrokCLIRequest(t, "question-tool", domain.GrokQuestionMethod)
				f.resource.DocumentJson, _ = json.Marshal(document)
				invoke = func(input string) (int, map[string]any) {
					return cliRun(t, root, []string{"interaction", "respond", "--id", id, "--revision", "1", "--request-id", request}, input)
				}
				requests = func() int { return len(f.requests) }
				closeOriginal = func() {
					document.Closure = domain.InteractionNativeClosed
					f.resource.DocumentJson, _ = json.Marshal(document)
					f.resource.Revision = 5
				}
				check = func() {
					if !proto.Equal(f.requests[0], f.requests[1]) {
						t.Fatal("question retry changed original wire identity")
					}
					var r domain.QuestionResponseInput
					if domain.Decode(f.requests[0].ResponseJson, &r) != nil || r.Grok.Outcome != domain.GrokQuestionCancelled {
						t.Fatal("native cancellation changed")
					}
				}
			} else {
				f := newApprovalCLIFixture(t)
				root, id = f.root, f.resource.Id
				document := originalGrokCLIRequest(t, "file-tool-write", domain.GrokFilePermissionMethod)
				f.resource.DocumentJson, _ = json.Marshal(document)
				invoke = func(input string) (int, map[string]any) {
					return cliRun(t, root, []string{"interaction", "approve", "--id", id, "--revision", "1", "--request-id", request}, input)
				}
				requests = func() int { return len(f.requests) }
				closeOriginal = func() {
					document.Closure = domain.InteractionNativeClosed
					f.resource.DocumentJson, _ = json.Marshal(document)
					f.resource.Revision = 5
				}
				check = func() {
					if !proto.Equal(f.requests[0], f.requests[1]) {
						t.Fatal("Write retry changed original wire identity")
					}
					var r domain.ApprovalResponseInput
					if domain.Decode(f.requests[0].ResponseJson, &r) != nil || r.Grok.Decision != domain.GrokAllowSession {
						t.Fatal("native session permission changed")
					}
				}
			}
			invalid := `{"grok":{"decision":"allow-once","outcome":"cancelled"}}`
			if code, _ := invoke(invalid); code == 0 || requests() != 0 {
				t.Fatal("wrong/mixed family crossed RPC")
			}
			input := `{"grok":{"decision":"allow-edits-session"}}`
			if question {
				input = `{"grok":{"outcome":"cancelled"}}`
			}
			if code, out := invoke(input); code != 0 || out["request_id"] != request {
				t.Fatal("original response", code, out)
			}
			closeOriginal()
			if code, out := invoke(input); code != 0 || out["result"].(map[string]any)["replayed"] != true || requests() != 2 {
				t.Fatal("closed original receipt", code, out)
			}
			check()
		})
	}
}
