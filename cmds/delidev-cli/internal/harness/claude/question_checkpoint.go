package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Questions have no remembered permission side effect. Their history proof
// nevertheless requires the original reply echo AND exact processed answers.
// Keep these separate from ordinary permission approvals and store no text.
type checkpointQuestionAnswer struct {
	checkpointToolApproval
	ResultDigest string `json:"processed_result_sha256"`
}

func inlineQuestionMetadata(raw []byte) ([sha256.Size]byte, bool) {
	var value struct {
		Questions []NativeQuestion   `json:"questions"`
		Answers   map[string]*string `json:"answers"`
	}
	if decodeNativeObject(raw, &value) != nil || len(value.Questions) == 0 || len(value.Questions) > 4 || value.Answers == nil {
		return [sha256.Size]byte{}, false
	}
	keys := map[string]bool{}
	for _, question := range value.Questions {
		if keys[question.Question] || domain.Text(question.Question, "native question", 4096, true) != nil || question.MultiSelect == nil || len(question.Options) < 2 || len(question.Options) > 4 {
			return [sha256.Size]byte{}, false
		}
		keys[question.Question] = true
	}
	for key, answer := range value.Answers {
		if !keys[key] || answer == nil || domain.Text(*answer, "native answer", domain.MaxMessageText, false) != nil {
			return [sha256.Size]byte{}, false
		}
	}
	digest, err := streamReplyDigest(raw)
	return digest, err == nil
}

func (b *ExecutionBinding) originalQuestionResult(toolID string, digest [sha256.Size]byte) bool {
	tool, exists := b.content.tools[toolID]
	if !exists || tool.name != string(inlineQuestionTool) || tool.parent != "" || digest == ([sha256.Size]byte{}) {
		return false
	}
	matches := 0
	for _, value := range b.interactions {
		if value == nil || value.request.ToolID != toolID {
			continue
		}
		if value.request.Kind != UserQuestion || !value.prepared || !value.echoed || value.canceled || value.behavior != PermissionAllow || value.input != tool.ownerInput || value.turn != tool.ownerTurn || value.questionResult != digest {
			return false
		}
		matches++
	}
	return matches == 1
}

func (b *ExecutionBinding) closedQuestionAnswers() ([]checkpointQuestionAnswer, error) {
	// The shared boundary validates every interaction, including foreign kinds,
	// duplicate ownership, released payloads and complete original inline tools.
	if _, err := b.closedToolApprovals(); err != nil {
		return nil, err
	}
	var result []checkpointQuestionAnswer
	for arrival, value := range b.interactions {
		if value.request.Kind == UserQuestion {
			result = append(result, checkpointQuestionAnswer{checkpointToolApproval{arrival, value.request.RequestID, value.request.ToolID, value.input, value.turn, hex.EncodeToString(value.requestDigest[:]), hex.EncodeToString(value.reply[:])}, hex.EncodeToString(value.questionResult[:])})
		}
	}
	slices.SortFunc(result, func(a, b checkpointQuestionAnswer) int { return strings.Compare(string(a.Arrival), string(b.Arrival)) })
	return result, nil
}

func (cp sessionCheckpoint) validateQuestionAnswers(requests, approved map[string]bool) error {
	if len(cp.QuestionAnswers) != len(cp.QuestionTools) {
		return historyUncertain()
	}
	tools := map[string]checkpointInlineTool{}
	arrivals := map[domain.ID]bool{}
	for _, proof := range cp.ToolApprovals {
		arrivals[proof.Arrival] = true
	}
	for _, tool := range cp.QuestionTools {
		tools[tool.ID] = tool
	}
	for i, answer := range cp.QuestionAnswers {
		tool, exists := tools[answer.Tool]
		if answer.Arrival.Validate() != nil || arrivals[answer.Arrival] || (i > 0 && cp.QuestionAnswers[i-1].Arrival >= answer.Arrival) || domain.Text(answer.Request, "native request identity", 128, true) != nil || requests[answer.Request] || approved[answer.Tool] || !exists || answer.Input != tool.Input || answer.Turn != tool.Turn || !validHistoryDigest(answer.RequestDigest) || !validHistoryDigest(answer.ReplyDigest) || answer.RequestDigest == strings.Repeat("0", 64) || answer.ReplyDigest == strings.Repeat("0", 64) || answer.ResultDigest != tool.MetadataDigest || !validHistoryDigest(answer.ResultDigest) || answer.ResultDigest == strings.Repeat("0", 64) {
			return historyUncertain()
		}
		requests[answer.Request], approved[answer.Tool], arrivals[answer.Arrival] = true, true, true
	}
	return nil
}

func restoreQuestionAnswers(b *ExecutionBinding, answers []checkpointQuestionAnswer) {
	if len(answers) != 0 && b.interactions == nil {
		b.interactions = map[domain.ID]*interactionState{}
	}
	for _, answer := range answers {
		value := &interactionState{input: answer.Input, turn: answer.Turn, request: NativeInteraction{Kind: UserQuestion, RequestID: answer.Request, ToolID: answer.Tool}, event: StreamEvent{Kind: NativeRequest, RequestID: answer.Request, ArrivalID: answer.Arrival}, prepared: true, echoed: true, behavior: PermissionAllow}
		request, _ := hex.DecodeString(answer.RequestDigest)
		reply, _ := hex.DecodeString(answer.ReplyDigest)
		result, _ := hex.DecodeString(answer.ResultDigest)
		copy(value.requestDigest[:], request)
		copy(value.reply[:], reply)
		copy(value.questionResult[:], result)
		b.interactions[answer.Arrival] = value
	}
}
