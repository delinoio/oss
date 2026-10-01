package grok

import (
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type planInteractionStage string

const (
	planPermissionPending  planInteractionStage = "permission-pending"
	planPermissionResolved planInteractionStage = "permission-resolved"
	planApprovalPending    planInteractionStage = "plan-approval-pending"
	planApprovalResolved   planInteractionStage = "plan-approval-resolved"
)

// PlanFileOrigin describes an original native Plan scope/revision. It is not a
// file approval, and cannot authorize a path or a write in a replacement input.
type PlanFileOrigin struct {
	EntryToolID  string
	EntryEventID string
	Revision     uint64
}

type planArtifact struct {
	origin        PlanFileOrigin
	contentDigest [32]byte
	writeToolID   string
}

type planToolState struct {
	name         fileToolName
	index        uint64
	arguments    string
	phase        fileToolPhase
	stage        planInteractionStage
	priorMode    NativeMode
	modeObserved bool
	arrival      domain.ID
	outcome      PlanOutcome
	artifact     planArtifact
}

type planFact struct {
	Delta       *fileToolDelta
	Observation *planObservation
	Interaction *struct {
		ID    string
		Stage planInteractionStage
	}
	Mode     *modeObservation
	Request  *planRequest
	Artifact *planArtifact
}

type planObserver struct {
	session    domain.ID
	prompt     string
	path       string
	pathPolicy planPathPolicy
	mode       NativeMode
	tools      map[string]planToolState
	active     string
	artifact   *planArtifact
	arrivals   map[domain.ID]bool
	requests   map[string]bool
	bytes      int
	lastEvent  uint64
	seenEvent  bool
}

func newPlanObserver(session domain.ID, prompt, home, workspace string, mode NativeMode) (*planObserver, error) {
	rel, err := historySessionPath(workspace, session)
	if err != nil || !nativeUUID(prompt, 4) || !planPath(home) || mode != NativeDefaultMode && mode != NativePlanMode {
		return nil, incompatible()
	}
	return &planObserver{session: session, prompt: prompt, path: filepath.Join(home, rel, "plan.md"), pathPolicy: nativePlanPath, mode: mode, tools: map[string]planToolState{}, arrivals: map[domain.ID]bool{}, requests: map[string]bool{}}, nil
}

func (o *planObserver) observe(event nativewire.Event) (planFact, error) {
	var fact planFact
	if o == nil || len(event.Params) > nativewire.MaxFrame || o.bytes+len(event.Params) > 4<<20 {
		return fact, domain.Fail(domain.ResourceExhausted, "Native Plan observations reached their bound.", "Retain the original plan and reconcile without replay.")
	}
	if event.Kind == nativewire.ServerRequest {
		key, err := fileToolRequestKey(event.ID)
		if event.Method != "_x.ai/exit_plan_mode" || event.Token.Validate() != nil || err != nil || o.arrivals[event.Token] || o.requests[key] {
			return fact, incompatible()
		}
		request, err := parsePlanRequest(event.Params, o.session)
		prior, exists := o.tools[request.ID]
		if err != nil || !exists || o.active != request.ID || prior.name != exitPlanTool || prior.phase != fileToolDescribed || prior.stage != planApprovalPending || prior.arrival != "" || o.mode != NativePlanMode || o.artifact == nil || o.artifact.origin.Revision == 0 || o.artifact.contentDigest != historyValueDigest(request.Content) {
			return fact, incompatible()
		}
		prior.arrival, prior.artifact = event.Token, *o.artifact
		o.tools[request.ID] = prior
		o.arrivals[event.Token], o.requests[key] = true, true
		o.bytes += len(event.Params)
		artifact := prior.artifact
		fact.Request, fact.Artifact = &request, &artifact
		return fact, nil
	}
	if event.Kind != nativewire.Notification {
		return fact, incompatible()
	}
	var variant struct {
		Update struct {
			Kind string `json:"sessionUpdate"`
			ID   string `json:"toolCallId"`
		} `json:"update"`
	}
	if json.Unmarshal(event.Params, &variant) != nil {
		return fact, incompatible()
	}
	var id string
	var next planToolState
	var sequence uint64
	hasEvent := false
	var entered *planArtifact
	newMode := o.mode
	switch variant.Update.Kind {
	case "tool_call_delta_chunk":
		var delta fileToolDelta
		if event.Method != "_x.ai/session_notification" || decode(event.Params, &delta) != nil || delta.Session != o.session || delta.Update.Index >= 128 || !toolText(delta.Update.Arguments) || (delta.Update.ID == nil) != (delta.Update.Name == nil) {
			return fact, incompatible()
		}
		id = o.active
		if delta.Update.ID != nil {
			id = *delta.Update.ID
			if !text(id, 256) || *delta.Update.Name != enterPlanTool && *delta.Update.Name != exitPlanTool {
				return fact, incompatible()
			}
		}
		prior, exists := o.tools[id]
		if id == "" || o.active != "" && o.active != id || exists && (prior.phase != "" || prior.index != delta.Update.Index || delta.Update.Name != nil && prior.name != *delta.Update.Name) || len(prior.arguments)+len(delta.Update.Arguments) > 256<<10 {
			return fact, incompatible()
		}
		next = prior
		if !exists {
			if delta.Update.Name == nil || *delta.Update.Name == enterPlanTool && o.artifact != nil || *delta.Update.Name == exitPlanTool && (o.artifact == nil || o.mode != NativePlanMode) {
				return fact, incompatible()
			}
			next.name, next.index, next.priorMode = *delta.Update.Name, delta.Update.Index, o.mode
		}
		next.arguments += delta.Update.Arguments
		fact.Delta = &delta
	case "tool_call", "tool_call_update":
		if event.Method != "session/update" {
			return fact, incompatible()
		}
		id = variant.Update.ID
		prior, exists := o.tools[id]
		if !exists || o.active != id {
			return fact, incompatible()
		}
		observation, err := parsePlanObservation(event.Params, o.session, o.prompt, prior.name, o.pathPolicy)
		if err != nil {
			return fact, err
		}
		next = prior
		switch observation.Phase {
		case fileToolDeclared:
			if prior.phase != "" || decode([]byte(prior.arguments), &struct{}{}) != nil {
				return fact, incompatible()
			}
			next.arguments = ""
		case fileToolDescribed:
			if prior.phase != fileToolDeclared || prior.stage != planPermissionPending {
				return fact, incompatible()
			}
		case fileToolCompleted:
			if prior.phase != fileToolDescribed {
				return fact, incompatible()
			}
			if prior.name == enterPlanTool {
				if prior.stage != planPermissionResolved || observation.Entered == nil || observation.Entered.Path != o.path || o.mode != NativePlanMode || prior.modeObserved != (prior.priorMode == NativeDefaultMode) {
					return fact, incompatible()
				}
				entered = &planArtifact{origin: PlanFileOrigin{EntryToolID: id, EntryEventID: observation.Meta.Event}, contentDigest: historyValueDigest("")}
			} else {
				if prior.stage != planApprovalResolved || prior.arrival == "" || !prior.outcome.valid() || o.artifact == nil || prior.artifact != *o.artifact {
					return fact, incompatible()
				}
				if prior.outcome == PlanCancelled {
					if prior.modeObserved || o.mode != NativePlanMode || observation.Ready != nil {
						return fact, incompatible()
					}
				} else if !prior.modeObserved || o.mode != NativeDefaultMode {
					return fact, incompatible()
				}
				if prior.outcome == PlanApproved {
					if observation.Ready == nil || observation.Ready.Path != o.path || historyValueDigest(observation.Ready.Content) != prior.artifact.contentDigest {
						return fact, incompatible()
					}
				} else if observation.Ready != nil {
					return fact, incompatible()
				}
			}
		default:
			return fact, incompatible()
		}
		sequence, err = eventIndex(observation.Meta.Event, o.session)
		if err != nil {
			return fact, err
		}
		hasEvent, next.phase, fact.Observation = true, observation.Phase, &observation
	case string(fileInteractionPending), string(fileInteractionResolved):
		var interaction struct {
			Session domain.ID `json:"sessionId"`
			Update  struct {
				Kind fileInteractionKind `json:"sessionUpdate"`
				ID   string              `json:"tool_call_id"`
				Type *string             `json:"kind,omitempty"`
			} `json:"update"`
		}
		if event.Method != "_x.ai/session_notification" || decode(event.Params, &interaction) != nil || interaction.Session != o.session {
			return fact, incompatible()
		}
		id = interaction.Update.ID
		prior, exists := o.tools[id]
		if !exists || o.active != id {
			return fact, incompatible()
		}
		next = prior
		if interaction.Update.Kind == fileInteractionPending {
			if interaction.Update.Type == nil {
				return fact, incompatible()
			}
			switch *interaction.Update.Type {
			case "permission":
				if prior.phase != fileToolDeclared || prior.stage != "" {
					return fact, incompatible()
				}
				next.stage = planPermissionPending
			case "plan_approval":
				if prior.name != exitPlanTool || prior.phase != fileToolDescribed || prior.stage != planPermissionResolved {
					return fact, incompatible()
				}
				next.stage = planApprovalPending
			default:
				return fact, incompatible()
			}
		} else {
			if interaction.Update.Type != nil || prior.phase != fileToolDescribed {
				return fact, incompatible()
			}
			if prior.stage == planPermissionPending {
				next.stage = planPermissionResolved
			} else if prior.stage == planApprovalPending && prior.arrival != "" {
				next.stage = planApprovalResolved
			} else {
				return fact, incompatible()
			}
		}
		fact.Interaction = &struct {
			ID    string
			Stage planInteractionStage
		}{id, next.stage}
	case "current_mode_update":
		observation, err := parseModeObservation(event, o.session)
		id = o.active
		prior, exists := o.tools[id]
		if err != nil || !exists || prior.phase != fileToolDescribed || prior.modeObserved {
			return fact, incompatible()
		}
		if prior.name == enterPlanTool {
			if prior.stage != planPermissionResolved || o.mode != NativeDefaultMode || observation.Update.Mode != NativePlanMode {
				return fact, incompatible()
			}
		} else if prior.stage != planApprovalResolved || prior.outcome != PlanApproved && prior.outcome != PlanAbandoned || o.mode != NativePlanMode || observation.Update.Mode != NativeDefaultMode {
			return fact, incompatible()
		}
		next = prior
		next.modeObserved = true
		newMode = observation.Update.Mode
		sequence, err = eventIndex(observation.Meta.Event, o.session)
		if err != nil {
			return fact, err
		}
		hasEvent, fact.Mode = true, &observation
	default:
		return fact, incompatible()
	}
	if hasEvent && o.seenEvent && sequence <= o.lastEvent {
		return planFact{}, incompatible()
	}
	if _, exists := o.tools[id]; !exists && len(o.tools) >= 128 {
		return planFact{}, domain.Fail(domain.ResourceExhausted, "Native Plan tool count reached its bound.", "Retain the original input and reconcile without replay.")
	}
	o.tools[id], o.active, o.mode = next, id, newMode
	if next.phase == fileToolCompleted {
		o.active = ""
	}
	if entered != nil {
		o.artifact = entered
	}
	if hasEvent {
		o.lastEvent, o.seenEvent = sequence, true
	}
	o.bytes += len(event.Params)
	return fact, nil
}

func (o *planObserver) bindResponse(id string, outcome PlanOutcome) error {
	prior, exists := o.tools[id]
	if !exists || id != o.active || prior.name != exitPlanTool || prior.stage != planApprovalResolved || prior.outcome != "" || !outcome.valid() {
		return incompatible()
	}
	prior.outcome = outcome
	o.tools[id] = prior
	return nil
}

func (o *planObserver) fileOrigin(input fileToolInput) (*PlanFileOrigin, error) {
	if o == nil || o.mode != NativePlanMode {
		return nil, nil
	}
	if o.active != "" || o.artifact == nil || input.Path != o.path || input.Name != writeFileTool {
		return nil, incompatible()
	}
	origin := o.artifact.origin
	return &origin, nil
}

func (o *planObserver) commitWrite(id string, origin PlanFileOrigin, value *fileWriteOutput) error {
	if o == nil || o.mode != NativePlanMode || o.active != "" || o.artifact == nil || o.artifact.origin != origin || value == nil || value.Path != o.path || historyValueDigest(value.Old) != o.artifact.contentDigest || origin.Revision >= 128 || !text(id, 256) {
		return incompatible()
	}
	o.artifact.origin.Revision++
	o.artifact.contentDigest, o.artifact.writeToolID = historyValueDigest(value.New), id
	return nil
}

func (o *planObserver) settled() bool {
	if o == nil || o.active != "" {
		return false
	}
	for _, tool := range o.tools {
		if tool.phase != fileToolCompleted {
			return false
		}
	}
	return true
}
