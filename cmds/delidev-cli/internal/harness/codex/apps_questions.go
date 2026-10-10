// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Keep the native approval as a typed app question. It cannot borrow command,
// file-change or arbitrary MCP approval authority.
func publicAppQuestion(request *QuestionRequest) *domain.QuestionRequest {
	value := &domain.QuestionRequest{Blocking: request.Blocking, AutoResolutionMS: request.AutoResolutionMS, CodexApp: request.CodexApp, Questions: []domain.Question{}}
	for _, source := range request.Questions {
		q := domain.Question{ID: source.ID, Header: source.Header, Text: source.Text, Other: source.Other, Secret: source.Secret, Options: []domain.QuestionOption{}}
		for _, o := range source.Options {
			q.Options = append(q.Options, domain.QuestionOption{Label: o.Label, Description: o.Description})
		}
		value.Questions = append(value.Questions, q)
	}
	return value
}

// Only successful completion of the exact approved app call can prove the
// one-call Allow was consumed. A failed result, closed prompt or transmission
// alone retains uncertainty; raw MCP content is not a question-output echo.
func (c *Client) confirmAppQuestionLocked(turn domain.ID, tool *Tool) (*InteractionStatus, error) {
	if tool.Status != ToolCompleted {
		return nil, nil
	}
	var accepted *InteractionStatus
	for _, owned := range c.execution.interactions.arrivals {
		if owned.questions == nil || owned.questions.CodexApp == nil || owned.status.ItemID != tool.ID {
			continue
		}
		context := owned.questions.CodexApp
		if owned.status.TurnID != turn || !context.Identity.SameOriginal(tool.CodexApp.Identity) || owned.status.Accepted || owned.status.ResponseID == "" || owned.status.Delivery == QuestionNotSent || accepted != nil {
			return nil, incompatible()
		}
		// The native Prompt policy has no remembered permission or alternate route.
		// The retained digest must be the exact one-call Allow, not a Cancel.
		answers := nativeQuestionResponse{Answers: map[string]nativeQuestionAnswer{owned.questions.Questions[0].ID: {Answers: []string{domain.CodexAppApprovalAllow}}}}
		if !owned.matchesAppAnswer(answers) {
			return nil, incompatible()
		}
		owned.status.QuestionEvidence = domain.NativeCodexAppResult
		owned.confirmAcceptance("")
		status := owned.status
		accepted = &status
	}
	return accepted, nil
}

func (owned *trackedInteraction) matchesAppAnswer(answers nativeQuestionResponse) bool {
	raw, err := json.Marshal(answers)
	return err == nil && sha256.Sum256(raw) == owned.answerDigest
}
