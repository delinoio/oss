package grok

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// OpenOwnedPublicAPI composes the verified first-input profiles. Plain text
// retains its existing native close/history path; rich input has no continuation
// or closed-history authority.
func OpenOwnedPublicAPI(ctx context.Context, config APIExecutionConfig, record PlanningRecorders, closure func(context.Context, ClosureClaim) error, stop func(context.Context, StopClaim) error) (*OwnedAPI, error) {
	if closure == nil || stop == nil {
		return nil, apiConfigurationError()
	}
	a, err := OpenOwnedAPIWithPlanning(ctx, config, record)
	if err != nil {
		return nil, err
	}
	a.closure, a.stop = closure, stop
	return a, nil
}

func (a *OwnedAPI) RunPublic(ctx context.Context, request domain.ID, input string, emit func(context.Context, InputObservation) error) (PromptResult, error) {
	if a == nil || a.connection == nil || a.fileReply == nil || a.questionReply == nil || a.planReply == nil {
		return PromptResult{}, apiConfigurationError()
	}
	return a.connection.runInput(ctx, request, input, a.input, emit, publicInput)
}

type PublicObservation struct {
	ToolID         string
	Tool           *domain.GrokToolObservation
	Request        *domain.GrokInteractionRequest
	ResolvedToolID string
	Interaction    *domain.GrokNativeInteractionObservation
	Mode           *domain.GrokModeObservation
}

func publicMetadata(m toolObservationMeta) *domain.GrokToolMetadata {
	return &domain.GrokToolMetadata{EventID: m.Event, ContextTokens: strconv.FormatUint(m.Context, 10), TimestampMS: strconv.FormatUint(m.TimestampMS, 10), StreamStartMS: strconv.FormatUint(m.StreamMS, 10), TurnStartMS: strconv.FormatUint(m.TurnMS, 10)}
}

func publicQuestions(items []questionItem) []domain.GrokQuestion {
	result := make([]domain.GrokQuestion, 0, len(items))
	for _, q := range items {
		var multiple *bool
		_ = json.Unmarshal(q.MultiSelect, &multiple)
		options := make([]domain.QuestionOption, 0, len(q.Options))
		for _, o := range q.Options {
			options = append(options, domain.QuestionOption{Label: o.Label, Description: o.Description})
		}
		result = append(result, domain.GrokQuestion{Question: q.Question, Options: options, MultiSelect: multiple})
	}
	return result
}

// PublicObservationOf projects already validated original facts. It performs no
// native operation and cannot fabricate the original response/terminal evidence.
func PublicObservationOf(v InputObservation, mode domain.GrokMode) (PublicObservation, error) {
	var out PublicObservation
	var delta *fileToolDelta
	switch v.Kind {
	case InputFileTool:
		f := v.FileTool
		if f == nil {
			return out, incompatible()
		}
		delta = f.Delta
		if o := f.Observation; o != nil {
			g := domain.GrokToolObservation{Name: domain.GrokToolName(o.Input.Name), Phase: domain.GrokToolPhase(o.Phase), Metadata: publicMetadata(o.Meta), Path: o.Input.Path, Content: o.Input.Content, InheritedPermission: f.InheritedPermission}
			if f.PlanFile != nil {
				p := f.PlanFile
				g.PlanFile = &domain.GrokPlanFileOrigin{EntryToolID: p.EntryToolID, EntryEventID: p.EntryEventID, Revision: p.Revision}
				// Native Plan paths belong to the private runtime, not public file actions.
				g.Path = ""
			}
			if o.Read != nil {
				text := o.Read.Content
				g.Output = &text
			}
			if o.Write != nil {
				text, old := o.Write.Text, o.Write.Old
				g.Output, g.Old = &text, &old
			}
			if o.Failure != nil {
				text := *o.Failure
				g.Output = &text
			}
			out.ToolID, out.Tool = o.ID, &g
		}
		if v.Permission != nil {
			if f.Permission == nil {
				return out, incompatible()
			}
			o := v.Permission
			out.ToolID = o.ToolID
			out.Request = &domain.GrokInteractionRequest{Version: SupportedVersion, Kind: domain.GrokFilePermission, ArrivalID: o.ArrivalID, RequestDigest: o.RequestDigest, ProposalDigest: o.ProposalDigest, Mode: mode, ToolName: "write", Path: f.Permission.Tool.Input.Path, Content: f.Permission.Tool.Input.Content}
		}
		if f.Interaction != nil {
			stage := domain.GrokPermissionPending
			if f.Interaction.Kind == fileInteractionResolved {
				stage = domain.GrokPermissionResolved
			}
			out.Interaction = &domain.GrokNativeInteractionObservation{ToolID: f.Interaction.ID, Stage: stage}
		}
	case InputQuestion:
		f := v.Question
		if f == nil {
			return out, incompatible()
		}
		delta = f.Delta
		if o := f.Observation; o != nil {
			text := o.Message
			out.ToolID = o.ID
			out.Tool = &domain.GrokToolObservation{Name: domain.GrokAsk, Phase: domain.GrokToolPhase(o.Phase), Metadata: publicMetadata(o.Meta), Questions: publicQuestions(o.Questions)}
			if o.Phase == fileToolCompleted {
				out.Tool.Output = &text
			}
		}
		if v.QuestionOffer != nil {
			if f.Request == nil {
				return out, incompatible()
			}
			o := v.QuestionOffer
			out.ToolID = o.ToolID
			out.Request = &domain.GrokInteractionRequest{Version: SupportedVersion, Kind: domain.GrokQuestionInteraction, ArrivalID: o.ArrivalID, RequestDigest: o.RequestDigest, ProposalDigest: o.ProposalDigest, Mode: mode, ToolName: "ask_user_question", Questions: publicQuestions(f.Request.Questions)}
		}
		if f.Interaction != nil {
			out.Interaction = &domain.GrokNativeInteractionObservation{ToolID: f.Interaction.ID, Stage: domain.GrokNativeInteractionStage(f.Interaction.Stage)}
		}
	case InputPlan:
		f := v.Plan
		if f == nil {
			return out, incompatible()
		}
		delta = f.Delta
		if o := f.Observation; o != nil {
			text := o.Message
			out.ToolID = o.ID
			out.Tool = &domain.GrokToolObservation{Name: domain.GrokToolName(o.Name), Phase: domain.GrokToolPhase(o.Phase), Metadata: publicMetadata(o.Meta)}
			if o.Phase == fileToolCompleted {
				out.Tool.Output = &text
			}
			if o.Ready != nil {
				out.Tool.Content = o.Ready.Content
			}
		}
		if o := f.Mode; o != nil {
			out.Mode = &domain.GrokModeObservation{ToolID: f.ModeToolID, Mode: domain.GrokMode(o.Update.Mode), EventID: o.Meta.Event, TimestampMS: strconv.FormatUint(o.Meta.Timestamp, 10)}
		}
		if v.PlanOffer != nil {
			if f.Request == nil {
				return out, incompatible()
			}
			o := v.PlanOffer
			out.ToolID = o.ToolID
			out.Request = &domain.GrokInteractionRequest{Version: SupportedVersion, Kind: domain.GrokPlanApproval, ArrivalID: o.ArrivalID, RequestDigest: o.RequestDigest, ProposalDigest: o.ProposalDigest, Mode: mode, ToolName: "exit_plan_mode", Plan: &domain.GrokPlanRevision{EntryToolID: o.Origin.EntryToolID, EntryEventID: o.Origin.EntryEventID, Revision: o.Origin.Revision, WriteToolID: o.WriteToolID, Content: f.Request.Content, ContentDigest: o.ContentDigest}}
		}
		if f.Interaction != nil {
			out.Interaction = &domain.GrokNativeInteractionObservation{ToolID: f.Interaction.ID, Stage: domain.GrokNativeInteractionStage(f.Interaction.Stage)}
		}
	default:
		return out, incompatible()
	}
	if delta != nil {
		var name *domain.GrokToolName
		if delta.Update.Name != nil {
			n := domain.GrokToolName(*delta.Update.Name)
			name = &n
		}
		out.Tool = &domain.GrokToolObservation{Phase: domain.GrokArguments, Arguments: &domain.GrokArgumentChunk{Index: delta.Update.Index, ID: delta.Update.ID, Name: name, Text: delta.Update.Arguments}}
		if delta.Update.ID != nil {
			out.ToolID = *delta.Update.ID
			out.Tool.Name = *name
		}
	}
	return out, nil
}

type publicTerminal struct {
	settled completedText
	value   domain.GrokPublicTerminal
}

func (a *apiConnection) retainPublicTerminal(settled completedText, result PromptResult, turn TurnCompleted, prompt PromptCompleted, rejection *rejectedFileResult, mixed *mixedTools) (*publicTerminal, error) {
	mode := domain.GrokDefaultMode
	if mixed != nil && mixed.plans != nil {
		mode = domain.GrokMode(mixed.plans.mode)
	}
	u := result.Meta.Usage
	number := func(n uint64) string { return strconv.FormatUint(n, 10) }
	v := domain.GrokPublicTerminal{InputRequestID: settled.request, InputDigest: settled.bodyDigest, OutputDigest: hex.EncodeToString(settled.output[:]), TextChunks: uint32(len(settled.chunks)), NativeEventID: turn.Meta.Event, TimestampMS: number(turn.Meta.TimestampMS), Model: a.profile.model, Mode: mode, Outcome: domain.ExecutionSucceeded, Responses: number(u.Calls), Counts: domain.GrokResponseCounts{Input: number(u.Input), Output: number(u.Output), CachedRead: number(u.CachedRead), CacheCreation: number(u.CacheCreation), Reasoning: number(u.Reasoning)}, TotalTokens: number(u.Total), APIDurationMS: number(u.DurationMS), Turns: number(u.Turns)}
	if rejection != nil {
		v.Outcome = domain.ExecutionStopped
		v.PermissionRejected = true
	}
	// Keep all three independently matched facts before exposing any callback
	// copy. A digest does not replace matching in the original controller.
	raw, err := json.Marshal(struct {
		Result    PromptResult
		Turn      TurnCompleted
		Prompt    PromptCompleted
		Rejection *rejectedFileResult
	}{result, turn, prompt, rejection})
	if err != nil {
		return nil, incompatible()
	}
	v.NativeFactsDigest = domain.GrokDigest(raw)
	return &publicTerminal{settled: settled, value: v}, nil
}

// ClosePublic joins only this original runtime after its matching rich result.
// It deliberately supplies no native history or continuation checkpoint.
func (a *OwnedAPI) ClosePublic(ctx context.Context) (domain.GrokPublicTerminal, error) {
	if a == nil || a.connection == nil {
		return domain.GrokPublicTerminal{}, apiConfigurationError()
	}
	c := a.connection
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return domain.GrokPublicTerminal{}, domain.SafeError(ctx.Err())
	}
	p := c.publicTerminal
	if p == nil || c.completedText != nil || c.textControl() == nil || c.textControl().profile != publicInput {
		return domain.GrokPublicTerminal{}, sessionUncertain()
	}
	if p.value.CleanupJoined {
		return p.value, nil
	}
	if err := c.profile.checkInitialized(); err != nil {
		return domain.GrokPublicTerminal{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if !p.settled.idle {
		if err := c.waitStopIdle(bounded, &p.settled, p.settled.prompt); err != nil {
			return domain.GrokPublicTerminal{}, err
		}
	}
	if err := c.Close(); err != nil {
		return domain.GrokPublicTerminal{}, err
	}
	p.value.Idle, p.value.CleanupJoined = true, true
	if p.value.Validate(string(c.session)) != nil {
		return domain.GrokPublicTerminal{}, sessionUncertain()
	}
	return p.value, nil
}
