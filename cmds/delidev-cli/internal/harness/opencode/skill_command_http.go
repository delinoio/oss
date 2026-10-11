// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type skillCommandHTTP struct {
	digest string
	done   chan skillCommandHTTPResult
	cancel context.CancelFunc
	joined bool
	result skillCommandHTTPResult
}
type skillCommandHTTPResult struct {
	raw []byte
	err error
}

// StartSkillCommand subscribes before consuming one original command claim.
// Native command HTTP is synchronous; the original owned operation runs beside
// the event reader. Its response cannot establish user-input acceptance or grant
// another send. Cancellation retains the uncertain claim and original process.
func (a *OwnedAPI) StartSkillCommand(ctx context.Context, request domain.ID, input domain.SessionInput) (InputReceipt, error) {
	if !a.valid() || a.session.apiProfile == nil {
		return InputReceipt{}, sessionInvalid()
	}
	command, err := PrepareSkillCommand(input, a.session.apiProfile.Skills)
	if err != nil || command == nil {
		return InputReceipt{}, incompatible()
	}
	message, _, err := freshInputIDPair()
	if err != nil {
		return InputReceipt{}, err
	}
	body, err := encodeSkillCommand(a.session.apiProfile.Settings, message, *command)
	if err != nil {
		return InputReceipt{}, err
	}
	if _, err := a.session.openEvents(ctx); err != nil {
		return InputReceipt{}, err
	}
	s := a.session
	if err := s.enter(ctx); err != nil {
		return InputReceipt{}, err
	}
	if s.input != nil || s.problem != nil || s.creation == nil || request.Validate() != nil || request == s.creation.request || !s.freshCheckpointInput(request, message, "") {
		s.leave()
		return InputReceipt{}, sessionConflict()
	}
	if err := s.verifyInstructions(ctx, "skill-command"); err != nil {
		s.leave()
		return InputReceipt{}, err
	}
	if err := s.verifyNativeSkills(ctx); err != nil {
		s.leave()
		return InputReceipt{}, err
	}
	id, err := s.readSession(ctx)
	if err != nil {
		s.leave()
		return InputReceipt{}, err
	}
	s.input = &sessionInput{receipt: InputReceipt{RequestID: request, SessionID: id, MessageID: message}, digest: sha256.Sum256([]byte(command.Text)), skill: command}
	claim := SessionClaim{RequestID: request, Kind: SubmitSkillCommandMutation, SessionID: id, MessageID: message, BodyDigest: mutationDigest(body)}
	if err := s.claim(ctx, claim); err != nil {
		s.leave()
		return s.input.receipt, sessionUncertain()
	}
	operation, cancel := context.WithCancel(ctx)
	attempt := &skillCommandHTTP{cancel: cancel, digest: claim.BodyDigest, done: make(chan skillCommandHTTPResult, 1)}
	s.commandHTTP = attempt
	receipt := s.input.receipt
	s.leave()
	if _, err := s.observeInput(ctx, s.runtimeRoot); err != nil {
		cancel()
		attempt.done <- skillCommandHTTPResult{err: err}
		return receipt, err
	}
	// Exactly one goroutine sends exactly one consumed original body. Its buffered
	// result remains owned until native lifetime cleanup joins this operation.
	go func() {
		raw, _, err := s.request(operation, http.MethodPost, "/session/"+id+"/command", body, http.StatusOK)
		attempt.done <- skillCommandHTTPResult{raw: raw, err: err}
	}()
	return receipt, nil
}

// The native command allocates its own user part. Bind it only after exact
// original message, settings and expanded native bytes match the frozen claim.
func (s *sessionAPI) validateStoredSkillInput(ctx context.Context, raw []byte) (bool, error) {
	fields, err := shape(raw, []string{"info", "parts"}, nil)
	if err != nil {
		return false, err
	}
	message, err := decodeNativeMessage(fields["info"])
	input := s.input
	if err != nil || input == nil || input.skill == nil || message.User == nil || message.ID != input.receipt.MessageID || message.SessionID != input.receipt.SessionID || message.User.Agent != string(s.creation.settings.Agent) || message.User.Provider != s.creation.settings.Provider || message.User.Model != s.creation.settings.Model || message.User.Variant != nil || message.User.System != nil || message.User.Tools != nil || message.User.Format != nil {
		return false, sessionProblem()
	}
	var parts []json.RawMessage
	if domain.Decode(fields["parts"], &parts) != nil || parts == nil || len(parts) > 1 {
		return false, sessionProblem()
	}
	if len(parts) == 0 {
		return false, nil
	}
	part, err := decodeNativePart(parts[0])
	if err != nil || part.Kind != TextPartKind || part.Text == nil || part.Text.Timing != nil || part.Text.Synthetic != nil || part.Text.Ignored != nil || part.Text.Metadata != nil || part.MessageID != input.receipt.MessageID || part.SessionID != input.receipt.SessionID || sha256.Sum256([]byte(part.Text.Text)) != input.digest || input.receipt.PartID != "" && input.receipt.PartID != part.ID {
		return false, sessionProblem()
	}
	if !input.observedPart {
		// Observation cannot replay input. The owning Worker independently verifies
		// this digest against its immutable assignment before publishing acceptance.
		claim := SessionClaim{RequestID: domain.NewID(), Kind: ObserveSkillInputMutation, SessionID: input.receipt.SessionID, MessageID: input.receipt.MessageID, PartID: part.ID, InputRequestID: input.receipt.RequestID, BodyDigest: hex.EncodeToString(input.digest[:])}
		if err := s.claim(ctx, claim); err != nil {
			return false, sessionUncertain()
		}
		input.receipt.PartID = part.ID
		input.observedPart = true
	}
	return true, nil
}

func (s *sessionAPI) joinSkillCommandHTTP(ctx context.Context, cancel bool) error {
	attempt := s.commandHTTP
	if attempt == nil {
		return nil
	}
	if cancel && attempt.cancel != nil {
		attempt.cancel()
	}
	if !attempt.joined {
		select {
		case attempt.result = <-attempt.done:
			attempt.joined = true
		case <-ctx.Done():
			return unavailable()
		}
	}
	// Native event and exact stored-history evidence own completion independently
	// of HTTP response loss. The response remains private and is never replayed.
	clear(attempt.result.raw)
	attempt.result.raw = nil
	if !cancel && (s.observer == nil || s.observer.skillCommand == nil) {
		return sessionUncertain()
	}
	return nil
}
