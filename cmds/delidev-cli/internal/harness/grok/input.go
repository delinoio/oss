package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type inputDiagnosticStage string

const (
	inputDiagnosticStart              inputDiagnosticStage = "input-start"
	inputDiagnosticEnvelope           inputDiagnosticStage = "input-envelope"
	inputDiagnosticQueue              inputDiagnosticStage = "input-queue"
	inputDiagnosticUpdate             inputDiagnosticStage = "input-update"
	inputDiagnosticText               inputDiagnosticStage = "input-text"
	inputDiagnosticTurnTerminal       inputDiagnosticStage = "input-turn-terminal"
	inputDiagnosticInterruptedTurn    inputDiagnosticStage = "input-interrupted-turn"
	inputDiagnosticPassive            inputDiagnosticStage = "input-passive"
	inputDiagnosticPromptTerminal     inputDiagnosticStage = "input-prompt-terminal"
	inputDiagnosticActivity           inputDiagnosticStage = "input-activity"
	inputDiagnosticTerminalComparison inputDiagnosticStage = "input-terminal-comparison"
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
type inputProfile string

const (
	plainTextInput    inputProfile = "plain-text"
	readFileInput     inputProfile = "read-file"
	fileWriteInput    inputProfile = "file-write"
	questionInput     inputProfile = "question"
	mixedToolInput    inputProfile = "mixed-tools"
	planQuestionInput inputProfile = "plan-questions"
	planningInput     inputProfile = "planning"
)

func (p inputProfile) mixed() bool { return p == mixedToolInput || p == planningInput }

func (p inputProfile) questionsOnly() bool { return p == questionInput || p == planQuestionInput }

const (
	InputAccepted           ObservationKind = "input-accepted"
	InputText               ObservationKind = "input-text"
	InputTitle              ObservationKind = "input-title"
	InputCompleted          ObservationKind = "input-completed"
	StopSettled             ObservationKind = "stop-settled"
	InputFileTool           ObservationKind = "input-file-tool"
	InputResponse           ObservationKind = "input-response"
	InputPermissionRejected ObservationKind = "input-permission-rejected"
	InputQuestion           ObservationKind = "input-question"
	InputPlan               ObservationKind = "input-plan"
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
	Interruption   *InterruptedPromptResult
	Stop           *StopObservation
	FileTool       *fileToolFact
	Response       *responseUsage
	Permission     *FilePermissionOffer
	Rejection      *rejectedFileResult
	Question       *questionFact
	QuestionOffer  *QuestionOffer
	Plan           *planFact
	PlanOffer      *PlanOffer
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
	return a.runInput(ctx, request, input, record, emit, plainTextInput)
}

// RunReadFiles composes the original input with the closed default Read profile.
// Write/questions/replies/Stop and successful-history handoff remain excluded;
// neither a read-only native descriptor nor this method grants a sandbox.
func (a *apiConnection) RunReadFiles(ctx context.Context, request domain.ID, input string, record func(context.Context, InputClaim) error, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	return a.runInput(ctx, request, input, record, emit, readFileInput)
}

// RunFileTools composes original Read/Write with explicit once-only permission
// replies and their original remembered edit scope. Stop and native history
// require separate controller profiles.
func (a *apiConnection) RunFileTools(ctx context.Context, request domain.ID, input string, record func(context.Context, InputClaim) error, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	return a.runInput(ctx, request, input, record, emit, fileWriteInput)
}

// RunQuestions owns the original default-mode question profile. Declining a
// question preserves native continuation; it is not a product Stop operation.
func (a *apiConnection) RunQuestions(ctx context.Context, request domain.ID, input string, record func(context.Context, InputClaim) error, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	return a.runInput(ctx, request, input, record, emit, questionInput)
}

func (a *apiConnection) RunTools(ctx context.Context, request domain.ID, input string, record func(context.Context, InputClaim) error, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	return a.runInput(ctx, request, input, record, emit, mixedToolInput)
}

func (a *apiConnection) runInput(ctx context.Context, request domain.ID, input string, record func(context.Context, InputClaim) error, emit func(context.Context, InputObservation) error, profile inputProfile) (result PromptResult, returned error) {
	if profile != plainTextInput && profile != readFileInput && profile != fileWriteInput && !profile.questionsOnly() && !profile.mixed() {
		return result, apiConfigurationError()
	}
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
	if !a.ready || a.inputStarted || request == a.product || request == a.creationRequest || request == a.modeRequest {
		return result, sessionUncertain()
	}
	if profile == planQuestionInput || profile == planningInput && a.profile.mode == domain.PlanMode {
		if a.profile.mode != domain.PlanMode || a.modeBinding == nil || a.modeBinding.Validate() != nil {
			return result, sessionUncertain()
		}
	} else if a.profile.mode == domain.PlanMode || a.modeStarted {
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
	home, err := os.Lstat(filepath.Dir(a.profile.path))
	if err != nil || !home.IsDir() {
		return result, sessionUncertain()
	}
	a.inputStarted = true
	if err := record(ctx, claim); err != nil {
		return result, sessionUncertain()
	}
	diagnosticStage := inputDiagnosticStart
	a.activateText(request, profile)
	control := a.textControl()
	life, cancel := context.WithCancel(ctx)
	control.mu.Lock()
	control.inputDone = life.Done()
	control.mu.Unlock()
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
		stop := control.finish()
		if stop != nil {
			<-stop.done
			if returned != nil && domain.SafeError(returned).Code != domain.Canceled {
				control.mu.Lock()
				stop.observation.ProblemCode = domain.SafeError(returned).Code
				control.mu.Unlock()
			}
		}
		control.joinFileReplies()
		control.joinQuestionReplies()
		control.joinPlanReplies()
		<-done
		close(watchStop)
		<-watchDone
		if logger := a.inspection.Logger; logger != nil {
			if returned != nil && domain.SafeError(returned).Code == domain.Canceled && control.observation().CleanupJoined {
				logger.InfoContext(ctx, "Grok Build original text interrupted and cleaned up", "owner_id", a.inspection.OwnerID, "session_id", a.product, "input_id", request)
			} else if returned != nil {
				logger.WarnContext(ctx, "Grok Build original input failed", "owner_id", a.inspection.OwnerID, "session_id", a.product, "input_id", request, "input_profile", profile, "stage", diagnosticStage, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "Grok Build original input completed", "owner_id", a.inspection.OwnerID, "session_id", a.product, "input_id", request, "input_profile", profile)
			}
		}
	}()
	queue := inputQueue{session: a.session, text: input}
	settled := completedText{request: request, bodyDigest: digest, home: home}
	output := sha256.New()
	var rpc nativewire.Response
	var turn TurnCompleted
	var completed PromptCompleted
	var interruptedTurn InterruptedTurnCompleted
	var interruptedPrompt InterruptedPromptCompleted
	var rejectedTurn rejectedFileTurn
	var rejectedPrompt rejectedFileCompletion
	rejected, promptRejected := false, false
	interrupted, promptInterrupted := false, false
	var responseCounters responseUsage
	var accounting responseAccounting
	var fileTools *fileToolObserver
	var questions *questionObserver
	var mixed *mixedTools
	rpcObserved, turnObserved, promptObserved, responseObserved := false, false, false, false
	var lastEvent, lastChunk uint64
	hasEvent := false
	if a.modeBinding != nil {
		lastEvent, _ = eventIndex(a.modeBinding.EventID, a.session)
		hasEvent = true
	}
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
	publishMixed := func(event nativewire.Event) (returned error) {
		stage := "original-tool"
		defer func() {
			if returned != nil && a.inspection.Logger != nil {
				a.inspection.Logger.WarnContext(life, "Grok Build original tool observation failed", "owner_id", a.inspection.OwnerID, "input_id", request, "stage", stage, "code", domain.SafeError(returned).Code)
			}
		}()
		if mixed == nil || !queue.running || queue.cleared {
			return incompatible()
		}
		fact, err := mixed.observe(event)
		if err != nil {
			return err
		}
		if fact.Plan != nil {
			stage = "original-plan-response"
			observation := InputObservation{Kind: InputPlan, Plan: fact.Plan}
			if event.Kind == nativewire.ServerRequest {
				offer, err := control.offerPlan(event, *fact.Plan)
				if err != nil {
					return err
				}
				observation.PlanOffer = &offer
			} else {
				outcome, err := control.observePlanReply(life, *fact.Plan)
				if err != nil {
					return err
				}
				if outcome != "" {
					if err := mixed.plans.bindResponse(fact.Plan.Interaction.ID, outcome); err != nil {
						return err
					}
				}
			}
			if fact.Plan.Observation != nil {
				stage = "original-plan-event"
				if err := observeIndex(fact.Plan.Observation.Meta.Event); err != nil {
					return err
				}
			}
			if fact.Plan.Mode != nil {
				stage = "original-plan-mode"
				if err := observeIndex(fact.Plan.Mode.Meta.Event); err != nil {
					return err
				}
			}
			stage = "original-plan-publication"
			return publish(observation)
		}
		if fact.Question != nil {
			observation := InputObservation{Kind: InputQuestion, Question: fact.Question}
			if event.Kind == nativewire.ServerRequest {
				offer, err := control.offerQuestion(event, *fact.Question)
				if err != nil {
					return err
				}
				observation.QuestionOffer = &offer
			} else if err := control.observeQuestionReply(life, *fact.Question); err != nil {
				return err
			}
			if fact.Question.Observation != nil {
				if err := observeIndex(fact.Question.Observation.Meta.Event); err != nil {
					return err
				}
			}
			return publish(observation)
		}
		if fact.File == nil {
			return incompatible()
		}
		observation := InputObservation{Kind: InputFileTool, FileTool: fact.File}
		if event.Kind == nativewire.ServerRequest {
			offer, err := control.offerFilePermission(event, *fact.File)
			if err != nil {
				return err
			}
			observation.Permission = &offer
		} else {
			if err := control.observeFileReply(life, *fact.File); err != nil {
				return err
			}
			if policy := control.originalEditPolicy(); policy != "" {
				if err := mixed.files.observeEditPolicy(policy); err != nil {
					return err
				}
			}
		}
		if fact.File.Observation != nil {
			if err := observeIndex(fact.File.Observation.Meta.Event); err != nil {
				return err
			}
		}
		return publish(observation)
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
		diagnosticStage = inputDiagnosticEnvelope
		events++
		if events > 4096 {
			return result, domain.Fail(domain.ResourceExhausted, "Native input observations reached their bound.", "Retain the original input and reconcile its native runtime.")
		}
		if event.Kind == nativewire.ServerRequest {
			if profile.mixed() {
				if err := publishMixed(event); err != nil {
					return result, err
				}
				continue
			}
			if profile.questionsOnly() {
				if questions == nil || !queue.running || queue.cleared {
					return result, incompatible()
				}
				fact, err := questions.observe(event)
				if err != nil {
					return result, err
				}
				offer, err := control.offerQuestion(event, fact)
				if err != nil {
					return result, err
				}
				if err := publish(InputObservation{Kind: InputQuestion, Question: &fact, QuestionOffer: &offer}); err != nil {
					return result, err
				}
				continue
			}
			if profile != fileWriteInput || fileTools == nil || !queue.running || queue.cleared {
				return result, incompatible()
			}
			fact, err := fileTools.observe(event)
			if err != nil {
				return result, err
			}
			offer, err := control.offerFilePermission(event, fact)
			if err != nil {
				return result, err
			}
			if err := publish(InputObservation{Kind: InputFileTool, FileTool: &fact, Permission: &offer}); err != nil {
				return result, err
			}
			continue
		}
		if event.Kind != nativewire.Notification {
			return result, incompatible()
		}
		switch event.Method {
		case "_x.ai/queue/changed":
			diagnosticStage = inputDiagnosticQueue
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
				if profile.mixed() {
					if profile == planningInput {
						mode := NativeDefaultMode
						if a.modeBinding != nil {
							mode = NativePlanMode
						}
						mixed, err = newPlanningTools(a.session, queue.prompt, filepath.Dir(a.profile.path), a.workspace, mode)
					} else {
						mixed, err = newMixedTools(a.session, queue.prompt)
					}
					if err != nil {
						return result, err
					}
					fileTools, questions = mixed.files, mixed.questions
				} else if profile.questionsOnly() {
					mode := NativeDefaultMode
					if profile == planQuestionInput {
						mode = NativePlanMode
					}
					questions, err = newQuestionObserverForMode(a.session, queue.prompt, mode)
					if err != nil {
						return result, err
					}
				} else if profile != plainTextInput {
					fileTools, err = newFileToolObserver(a.session, queue.prompt)
					if err != nil {
						return result, err
					}
				}
				control.accept(queue.prompt)
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
			diagnosticStage = inputDiagnosticUpdate
			switch variant.Update.Kind {
			case "tool_call_delta_chunk", "tool_call", "tool_call_update", "pending_interaction", "interaction_resolved", "current_mode_update":
				if profile.mixed() {
					if err := publishMixed(event); err != nil {
						return result, err
					}
					continue
				}
				if profile.questionsOnly() {
					if questions == nil || !queue.running || queue.cleared {
						return result, incompatible()
					}
					fact, err := questions.observe(event)
					if err != nil {
						return result, err
					}
					if err := control.observeQuestionReply(life, fact); err != nil {
						return result, err
					}
					if fact.Observation != nil {
						if err := observeIndex(fact.Observation.Meta.Event); err != nil {
							return result, err
						}
					}
					if err := publish(InputObservation{Kind: InputQuestion, Question: &fact}); err != nil {
						return result, err
					}
					continue
				}
				if profile == plainTextInput || fileTools == nil || !queue.running || queue.cleared {
					return result, incompatible()
				}
				fact, err := fileTools.observe(event)
				if err != nil {
					return result, err
				}
				if profile == readFileInput && (fact.Delta != nil && fact.Delta.Update.Name != nil && *fact.Delta.Update.Name != readFileTool || fact.Observation != nil && fact.Observation.Input.Name != readFileTool || fact.Permission != nil) {
					return result, incompatible()
				}
				if profile == fileWriteInput {
					if err := control.observeFileReply(life, fact); err != nil {
						return result, err
					}
					if policy := control.originalEditPolicy(); policy != "" {
						if err := fileTools.observeEditPolicy(policy); err != nil {
							return result, err
						}
					}
				}
				if fact.Observation != nil {
					if err := observeIndex(fact.Observation.Meta.Event); err != nil {
						return result, err
					}
				}
				if err := publish(InputObservation{Kind: InputFileTool, FileTool: &fact}); err != nil {
					return result, err
				}
			case "agent_message_chunk":
				diagnosticStage = inputDiagnosticText
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
				// Retain original facts before exposing the callback's mutable copy.
				settled.chunks = append(settled.chunks, historyValueDigest(chunk))
				settled.lastChunk = historyTextEnvelopeDigest(chunk)
				_, _ = output.Write([]byte(chunk.Update.Content.Text))
				if err := publish(InputObservation{Kind: InputText, Chunk: &chunk}); err != nil {
					return result, err
				}
			case "turn_completed":
				diagnosticStage = inputDiagnosticTurnTerminal
				if event.Method != "_x.ai/session_notification" || turnObserved || !queue.running {
					return result, incompatible()
				}
				var reason struct {
					Update struct {
						Reason StopReason `json:"stop_reason"`
					} `json:"update"`
				}
				if json.Unmarshal(event.Params, &reason) != nil {
					return result, incompatible()
				}
				if reason.Update.Reason == Cancelled {
					if profile == fileWriteInput || profile.mixed() {
						rejectedTurn, err = parseRejectedFileTurn(event.Params, a.session, queue.prompt, a.profile.model)
						if err != nil {
							return result, err
						}
						if err := observeIndex(rejectedTurn.Turn.Meta.Event); err != nil {
							return result, err
						}
						rejected = true
					} else {
						if control.originalStop() == nil {
							return result, incompatible()
						}
						diagnosticStage = inputDiagnosticInterruptedTurn
						interruptedTurn, err = parseInterruptedTurn(event.Params, a.session, queue.prompt)
						if err != nil {
							return result, err
						}
						if err := observeIndex(interruptedTurn.Meta.Event); err != nil {
							return result, err
						}
						interrupted = true
					}
				} else {
					turn, err = parseTurnCompleted(event.Params, a.session, queue.prompt, a.profile.model)
					if err != nil {
						return result, err
					}
					if err := observeIndex(turn.Meta.Event); err != nil {
						return result, err
					}
				}
				turnObserved = true
			default:
				diagnosticStage = inputDiagnosticPassive

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
					if !queue.running || profile == plainTextInput && responseObserved {
						return result, incompatible()
					}
					responseCounters, responseObserved = observation.usage, true
					if err := accounting.observe(observation.usage); err != nil {
						return result, err
					}
					if err := publish(InputObservation{Kind: InputResponse, Response: &observation.usage}); err != nil {
						return result, err
					}
				case retryMetadata:
					if profile != plainTextInput || !queue.running || len(settled.chunks) == 0 || observation.retry == nil || observation.retry.Attempt != uint64(len(settled.retries)+1) {
						return result, incompatible()
					}
					settled.retries = append(settled.retries, *observation.retry)
					if logger := a.inspection.Logger; logger != nil {
						logger.InfoContext(life, "grok_native_retry_observed", "owner_id", a.inspection.OwnerID, "input_id", request, "attempt", observation.retry.Attempt, "error", observation.retry.Error)
					}
				case lastTurnMetadata:
					if !queue.cleared || settled.summarySeen {
						return result, incompatible()
					}
					settled.summary, settled.summarySeen = observation.text, true
				}
			}
		case "_x.ai/session/prompt_complete":
			diagnosticStage = inputDiagnosticPromptTerminal
			if promptObserved || !queue.running {
				return result, incompatible()
			}
			var reason struct {
				Reason StopReason `json:"stopReason"`
			}
			if json.Unmarshal(event.Params, &reason) != nil {
				return result, incompatible()
			}
			if reason.Reason == Cancelled {
				if profile == fileWriteInput || profile.mixed() {
					rejectedPrompt, err = parseRejectedFileCompletion(event.Params, a.session, queue.prompt)
					if err != nil {
						return result, err
					}
					promptRejected = true
				} else {
					if control.originalStop() == nil {
						return result, incompatible()
					}
					interruptedPrompt, err = parseInterruptedPromptCompleted(event.Params, a.session, queue.prompt)
					if err != nil {
						return result, err
					}
					promptInterrupted = true
				}
			} else {
				completed, err = parsePromptCompleted(event.Params, a.session, queue.prompt)
				if err != nil {
					return result, err
				}
			}
			promptObserved = true
		case "_x.ai/sessions/changed":
			diagnosticStage = inputDiagnosticActivity
			activity, err := parseActivity(event.Params, a.session, a.workspace)
			if err != nil {
				return result, err
			}
			if activity == idleActivity && (queue.cleared || control.originalStop() != nil) {
				if settled.idle {
					return result, incompatible()
				}
				settled.idle = true
			} else if settled.idle {
				return result, incompatible()
			}
		default:
			return result, incompatible()
		}
	}
	diagnosticStage = inputDiagnosticTerminalComparison
	if interrupted != promptInterrupted || rejected != promptRejected {
		return result, incompatible()
	}
	if profile == planningInput && (mixed == nil || !control.plansSettled(mixed.plans)) {
		return result, incompatible()
	}
	if rejected {
		if !queue.cleared || !responseObserved || !control.filesSettled(fileTools, true) || profile.mixed() && !control.questionsSettled(questions) {
			return result, incompatible()
		}
		denied, err := parseRejectedFileResult(rpc.Result, a.session, queue.prompt, a.profile.model, accounting)
		if err != nil || matchFileRejection(denied, rejectedTurn, rejectedPrompt, a.profile.model) != nil {
			return result, incompatible()
		}
		control.finish()
		if err := a.waitStopIdle(life, &settled, queue.prompt); err != nil {
			return result, err
		}
		if err := a.profile.checkInitialized(); err != nil {
			return result, err
		}
		if err := a.Close(); err != nil {
			return result, sessionUncertain()
		}
		// Owned cleanup cancels the native publication context. Publish the
		// correlated rejection on the original caller context after joining it.
		if err := emit(ctx, InputObservation{Kind: InputPermissionRejected, InputID: request, NativePromptID: queue.prompt, Rejection: &denied}); err != nil {
			return result, sessionUncertain()
		}
		return denied.Result, domain.Fail(domain.Canceled, "The original Grok Build file permission was rejected.", "Retain its reported usage and reconcile history before resuming.")
	}
	if interrupted {
		stopped, err := parseInterruptedPromptResult(rpc.Result, a.session, queue.prompt, a.profile.model)
		if err != nil || matchInterruption(stopped, interruptedTurn, interruptedPrompt) != nil {
			return result, incompatible()
		}
		control.finish()
		if err := a.waitStopIdle(life, &settled, queue.prompt); err != nil {
			return result, err
		}
		if err := a.profile.checkInitialized(); err != nil {
			return result, err
		}
		if err := a.settleStop(life, control, Cancelled, MidTurnAbort); err != nil {
			return result, err
		}
		copy(settled.output[:], output.Sum(nil))
		if err := a.retainStoppedText(settled, &InterruptedTextTerminal{Result: stopped, Turn: interruptedTurn, Prompt: interruptedPrompt}); err != nil {
			return result, err
		}
		observation := control.observation()
		if err := emit(ctx, InputObservation{Kind: StopSettled, InputID: request, NativePromptID: queue.prompt, Interruption: &stopped, Stop: &observation}); err != nil {
			return result, sessionUncertain()
		}
		return result, domain.Fail(domain.Canceled, "The original Grok Build input was interrupted.", "Resume only after the original stopped history is reconciled.")
	}
	if !queue.cleared || !responseObserved {
		return result, incompatible()
	}
	if profile != plainTextInput {
		if (profile.questionsOnly() || profile.mixed()) && !control.questionsSettled(questions) || !profile.questionsOnly() && (fileTools == nil || profile == readFileInput && len(fileTools.tools) > 0 && !fileTools.settled() || (profile == fileWriteInput || profile.mixed()) && !control.filesSettled(fileTools, false)) {
			return result, incompatible()
		}
		result, err = parseFilePromptResult(rpc.Result, a.session, queue.prompt, a.profile.model, accounting)
	} else {
		result, err = parsePromptResult(rpc.Result, a.session, queue.prompt, a.profile.model)
	}
	if err != nil {
		return result, err
	}
	if err := matchCompletion(result, turn, completed, a.profile.model); err != nil {
		return result, err
	}
	usage := result.Meta.Usage
	if result.Reason != EndTurn || profile == plainTextInput && (usage.Calls != 1 || usage.Turns != 1 || responseCounters != (responseUsage{Input: usage.Input, Output: usage.Output, CachedRead: usage.CachedRead, CacheCreation: usage.CacheCreation, Reasoning: usage.Reasoning})) {
		return result, incompatible()
	}
	if err := a.profile.checkInitialized(); err != nil {
		return result, err
	}
	if profile == plainTextInput {
		facts, err := retainTextTerminal(result, turn, completed)
		if err != nil {
			return result, err
		}
		settled.terminalFacts = &facts
	}
	settled.terminal = historyValueDigest(turn)
	settled.usage, _ = validateUsage(usage, a.profile.model)
	copy(settled.output[:], output.Sum(nil))
	if stop := control.finish(); stop != nil {
		// A real successful terminal may race an original Stop notification.
		// Preserve completion while separately proving stopped ownership; this
		// forced cleanup cannot acquire successful history/closure authority.
		if err := a.waitStopIdle(life, &settled, queue.prompt); err != nil {
			return result, err
		}
		if err := a.settleStop(life, control, EndTurn, ""); err != nil {
			return result, err
		}
		if err := a.retainStoppedText(settled, nil); err != nil {
			return result, err
		}
		if err := emit(ctx, InputObservation{Kind: InputCompleted, InputID: request, NativePromptID: queue.prompt, Result: &result}); err != nil {
			return result, sessionUncertain()
		}
		observation := control.observation()
		if err := emit(ctx, InputObservation{Kind: StopSettled, InputID: request, NativePromptID: queue.prompt, Stop: &observation}); err != nil {
			return result, sessionUncertain()
		}
		return result, nil
	}
	if len(settled.retries) != 0 {
		return result, incompatible()
	}
	if err := publish(InputObservation{Kind: InputCompleted, Result: &result}); err != nil {
		return result, err
	}
	settled.prompt = queue.prompt
	if profile == plainTextInput {
		a.completedText = &settled
	}
	return result, nil
}
