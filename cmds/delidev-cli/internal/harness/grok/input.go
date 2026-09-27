package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type InputPhase string

const (
	ClaimInput InputPhase = "claim-input"
	BindInput  InputPhase = "bind-input"
)

type InputClaim struct {
	Phase            InputPhase `json:"phase"`
	RequestID        domain.ID  `json:"request_id"`
	ProductSessionID domain.ID  `json:"product_session_id"`
	NativeSessionID  domain.ID  `json:"native_session_id"`
	NativePromptID   string     `json:"native_prompt_id,omitempty"`
	BodyDigest       string     `json:"body_digest"`
}

func (c InputClaim) Validate() error {
	if c.RequestID.Validate() != nil || c.ProductSessionID.Validate() != nil || c.NativeSessionID.Validate() != nil || c.RequestID == c.ProductSessionID {
		return apiConfigurationError()
	}
	digest, err := hex.DecodeString(c.BodyDigest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != c.BodyDigest {
		return apiConfigurationError()
	}
	if c.Phase == ClaimInput && c.NativePromptID == "" || c.Phase == BindInput && nativeUUID(c.NativePromptID, 4) {
		return nil
	}
	return apiConfigurationError()
}

type ObservationKind string

const (
	InputAccepted  ObservationKind = "input-accepted"
	InputText      ObservationKind = "input-text"
	InputTitle     ObservationKind = "input-title"
	InputCompleted ObservationKind = "input-completed"
)

// InputObservation contains native facts only. Its coordinator must journal
// an original outbox identity before publication and retain uncertain delivery.
type InputObservation struct {
	Kind           ObservationKind
	InputID        domain.ID
	NativePromptID string
	Chunk          *TextChunk
	Title          string
	Result         *PromptResult
}

type promptParams struct {
	Session domain.ID    `json:"sessionId"`
	Prompt  []promptText `json:"prompt"`
}
type promptText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// TextInputClaimDigest lets the owning Worker compare an input claim against
// its immutable assignment without persisting the original prompt a second time.
func TextInputClaimDigest(session domain.ID, input string) (string, error) {
	if session.Validate() != nil || domain.Text(input, "original native input", 256<<10, true) != nil || strings.HasPrefix(strings.TrimSpace(input), "/") {
		return "", apiConfigurationError()
	}
	body, err := json.Marshal(promptParams{Session: session, Prompt: []promptText{{Type: "text", Text: input}}})
	if err != nil {
		return "", apiConfigurationError()
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// RunText owns the first plain-text input boundary. It has no permission reply,
// slash-command, subsequent-input or restoration authority. Successful return
// proves original root-turn completion, not auxiliary/history/process cleanup.
func (a *apiConnection) RunText(ctx context.Context, request domain.ID, input string, record func(context.Context, InputClaim) error, emit func(context.Context, InputObservation) error) (result PromptResult, returned error) {
	if request.Validate() != nil || domain.Text(input, "original native input", 256<<10, true) != nil || record == nil || emit == nil {
		return result, apiConfigurationError()
	}
	// ACP command interpretation needs a separate positive native profile.
	// Do not let an ordinary input silently change accepted permission/options.
	if strings.HasPrefix(strings.TrimSpace(input), "/") {
		return result, incompatible()
	}
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return result, domain.SafeError(ctx.Err())
	}
	if !a.ready || a.inputStarted || request == a.product || request == a.creationRequest {
		return result, sessionUncertain()
	}
	if err := a.profile.checkInitialized(); err != nil {
		return result, err
	}
	inspection, stopInspection := context.WithTimeout(ctx, 15*time.Second)
	err := inspectConfiguration(inspection, a.inspection, a.profile.path)
	stopInspection()
	if err != nil {
		return result, err
	}
	if err := a.profile.checkInitialized(); err != nil {
		return result, err
	}
	params := promptParams{Session: a.session, Prompt: []promptText{{Type: "text", Text: input}}}
	encoded, err := json.Marshal(struct {
		JSONRPC string       `json:"jsonrpc"`
		ID      domain.ID    `json:"id"`
		Method  string       `json:"method"`
		Params  promptParams `json:"params"`
	}{"2.0", request, "session/prompt", params})
	if err != nil || len(encoded) > nativewire.MaxFrame {
		return result, domain.Fail(domain.ResourceExhausted, "The encoded native input exceeds its bound.", "Reduce the original input before accepting native work.")
	}
	digest, err := TextInputClaimDigest(a.session, input)
	if err != nil {
		return result, err
	}
	claim := InputClaim{Phase: ClaimInput, RequestID: request, ProductSessionID: a.product, NativeSessionID: a.session, BodyDigest: digest}
	a.inputStarted = true
	if err := record(ctx, claim); err != nil {
		return result, sessionUncertain()
	}
	life, cancel := context.WithCancel(ctx)
	read, wake := context.WithCancel(life)
	watchStop, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-a.wire.Done():
			// Publication may be waiting on the coordinator while the native
			// process exits. It must share that runtime's cancellation boundary.
			cancel()
		case <-watchStop:
		}
	}()
	type reply struct {
		response nativewire.Response
		err      error
	}
	replies := make(chan reply, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := a.wire.Call(life, request, "session/prompt", params)
		replies <- reply{response, err}
		wake()
	}()
	defer func() {
		cancel()
		wake()
		if returned != nil {
			if err := a.Close(); err != nil {
				returned = sessionUncertain()
			}
		}
		<-done
		close(watchStop)
		<-watchDone
		if logger := a.inspection.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "Grok Build original text input failed", "owner_id", a.inspection.OwnerID, "session_id", a.product, "input_id", request, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "Grok Build original text input completed", "owner_id", a.inspection.OwnerID, "session_id", a.product, "input_id", request)
			}
		}
	}()
	queue := inputQueue{session: a.session, text: input}
	var rpc nativewire.Response
	var turn TurnCompleted
	var completed PromptCompleted
	var responseCounters responseUsage
	rpcObserved, turnObserved, promptObserved, responseObserved := false, false, false, false
	var lastEvent, lastChunk uint64
	hasEvent := false
	textBytes, events := 0, 0
	observeIndex := func(event string) error {
		if event == "" {
			return nil
		}
		index, err := eventIndex(event, a.session)
		if err != nil || hasEvent && index <= lastEvent {
			return incompatible()
		}
		lastEvent, hasEvent = index, true
		return nil
	}
	publish := func(observation InputObservation) error {
		observation.InputID, observation.NativePromptID = request, queue.prompt
		if err := emit(life, observation); err != nil {
			return sessionUncertain()
		}
		return nil
	}
	for !rpcObserved || !turnObserved || !promptObserved {
		if !rpcObserved {
			select {
			case reply := <-replies:
				if reply.err != nil || reply.response.ErrorCode != nil {
					return result, sessionUncertain()
				}
				rpc, rpcObserved = reply.response, true
			default:
			}
		}
		if rpcObserved && turnObserved && promptObserved {
			break
		}
		reader := read
		if rpcObserved {
			reader = life
		}
		event, err := a.wire.Next(reader)
		if err != nil {
			// The original RPC response wakes a blocked reader without removing
			// an event or starting another consumer. No polling/overread occurs.
			if !rpcObserved && read.Err() != nil && life.Err() == nil {
				continue
			}
			return result, sessionUncertain()
		}
		events++
		if events > 4096 {
			return result, domain.Fail(domain.ResourceExhausted, "Native input observations reached their bound.", "Retain the original input and reconcile its native runtime.")
		}
		if event.Kind != nativewire.Notification {
			return result, incompatible()
		}
		switch event.Method {
		case "_x.ai/queue/changed":
			queued, running := queue.queued, queue.running
			if err := queue.observe(event.Params); err != nil {
				return result, err
			}
			if !queued && queue.queued {
				claim.Phase, claim.NativePromptID = BindInput, queue.prompt
				if err := record(life, claim); err != nil {
					return result, sessionUncertain()
				}
			}
			if !running && queue.running {
				if err := publish(InputObservation{Kind: InputAccepted}); err != nil {
					return result, err
				}
			}
		case "session/update", "_x.ai/session_notification":
			var variant struct {
				Update struct {
					Kind string `json:"sessionUpdate"`
				} `json:"update"`
			}
			if json.Unmarshal(event.Params, &variant) != nil {
				return result, incompatible()
			}
			switch variant.Update.Kind {
			case "agent_message_chunk":
				chunk, err := parseTextChunk(event.Params, a.session, queue.prompt)
				if err != nil || event.Method != "session/update" || !queue.running || queue.cleared || chunk.Meta.Chunk <= lastChunk {
					return result, incompatible()
				}
				if err := observeIndex(chunk.Meta.Event); err != nil {
					return result, err
				}
				textBytes += len(chunk.Update.Content.Text)
				if textBytes > 4<<20 {
					return result, domain.Fail(domain.ResourceExhausted, "Native text exceeded its retained input bound.", "Retain the original output and reconcile this input without replay.")
				}
				lastChunk = chunk.Meta.Chunk
				if err := publish(InputObservation{Kind: InputText, Chunk: &chunk}); err != nil {
					return result, err
				}
			case "turn_completed":
				if event.Method != "_x.ai/session_notification" || turnObserved || !queue.running {
					return result, incompatible()
				}
				turn, err = parseTurnCompleted(event.Params, a.session, queue.prompt, a.profile.model)
				if err != nil {
					return result, err
				}
				if err := observeIndex(turn.Meta.Event); err != nil {
					return result, err
				}
				turnObserved = true
			default:
				observation, err := parsePassiveObservation(event.Params, event.Method, a.session, queue.prompt)
				if err != nil {
					return result, err
				}
				if err := observeIndex(observation.event); err != nil {
					return result, err
				}
				switch observation.kind {
				case titleMetadata:
					if !queue.running {
						return result, incompatible()
					}
					if err := publish(InputObservation{Kind: InputTitle, Title: observation.text}); err != nil {
						return result, err
					}
				case responseMetadata:
					if !queue.running || responseObserved {
						return result, incompatible()
					}
					responseCounters, responseObserved = observation.usage, true
				}
			}
		case "_x.ai/session/prompt_complete":
			if promptObserved || !queue.running {
				return result, incompatible()
			}
			completed, err = parsePromptCompleted(event.Params, a.session, queue.prompt)
			if err != nil {
				return result, err
			}
			promptObserved = true
		case "_x.ai/sessions/changed":
			if _, err := parseActivity(event.Params, a.session, a.workspace); err != nil {
				return result, err
			}
		default:
			return result, incompatible()
		}
	}
	if !queue.cleared || !responseObserved {
		return result, incompatible()
	}
	result, err = parsePromptResult(rpc.Result, a.session, queue.prompt, a.profile.model)
	if err != nil {
		return result, err
	}
	if err := matchCompletion(result, turn, completed, a.profile.model); err != nil {
		return result, err
	}
	usage := result.Meta.Usage
	if result.Reason != EndTurn || usage.Calls != 1 || usage.Turns != 1 || responseCounters != (responseUsage{Input: usage.Input, Output: usage.Output, CachedRead: usage.CachedRead, CacheCreation: usage.CacheCreation, Reasoning: usage.Reasoning}) {
		return result, incompatible()
	}
	if err := a.profile.checkInitialized(); err != nil {
		return result, err
	}
	if err := publish(InputObservation{Kind: InputCompleted, Result: &result}); err != nil {
		return result, err
	}
	return result, nil
}
