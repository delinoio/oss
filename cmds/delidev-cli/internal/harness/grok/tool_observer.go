package grok

import (
	"encoding/json"
	"regexp"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

var toolIntegerRequest = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,18})$`)

func fileToolRequestKey(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > 256 {
		return "", incompatible()
	}
	if raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil || !text(value, 128) {
			return "", incompatible()
		}
		return "s:" + value, nil
	}
	if toolIntegerRequest.Match(raw) {
		return "n:" + string(raw), nil
	}
	return "", incompatible()
}

type fileToolState struct {
	name       fileToolName
	index      uint64
	arguments  string
	input      fileToolInput
	phase      fileToolPhase
	pending    bool
	resolved   bool
	permission domain.ID
	inherited  domain.ID
	details    [32]byte
	plan       *PlanFileOrigin
}

// fileToolObserver belongs to one already accepted native prompt. It is a
// single-reader observation boundary, not a sender, response claim or outbox.
// Retain only original comparison facts; caller mutation cannot replace them.
type fileToolObserver struct {
	session    domain.ID
	prompt     string
	tools      map[string]fileToolState
	arrivals   map[domain.ID]bool
	requests   map[string]bool
	bytes      int
	lastEvent  uint64
	seenEvent  bool
	editPolicy domain.ID
	plans      *planObserver
}

type fileToolFact struct {
	Delta       *fileToolDelta
	Observation *fileToolObservation
	Interaction *fileInteraction
	Permission  *filePermission
	// This is original controller provenance, never a fabricated native reply.
	InheritedPermission domain.ID
	PlanFile            *PlanFileOrigin
}

func newFileToolObserver(session domain.ID, prompt string) (*fileToolObserver, error) {
	if session.Validate() != nil || !nativeUUID(prompt, 4) {
		return nil, incompatible()
	}
	return &fileToolObserver{session: session, prompt: prompt, tools: map[string]fileToolState{}, arrivals: map[domain.ID]bool{}, requests: map[string]bool{}}, nil
}

func (o *fileToolObserver) observe(event nativewire.Event) (fileToolFact, error) {
	var fact fileToolFact
	if o == nil || len(event.Params) > nativewire.MaxFrame || o.bytes+len(event.Params) > 4<<20 {
		return fact, domain.Fail(domain.ResourceExhausted, "Native file-tool observations reached their bound.", "Retain the original tool and reconcile its native input without replay.")
	}
	if event.Kind == nativewire.ServerRequest {
		requestKey, err := fileToolRequestKey(event.ID)
		if event.Method != "session/request_permission" || event.Token.Validate() != nil || err != nil || o.arrivals[event.Token] || o.requests[requestKey] {
			return fact, incompatible()
		}
		permission, err := parseFilePermission(event.Params, o.session)
		if err != nil {
			return fact, err
		}
		prior, exists := o.tools[permission.Tool.ID]
		if !exists || prior.phase != fileToolDescribed || !prior.pending || prior.resolved || prior.permission != "" || prior.input != permission.Tool.Input || prior.details != historyValueDigest(permission.Tool) {
			return fact, incompatible()
		}
		prior.permission, prior.inherited = event.Token, ""
		o.tools[permission.Tool.ID] = prior
		o.arrivals[event.Token], o.requests[requestKey] = true, true
		o.bytes += len(event.Params)
		fact.Permission = &permission
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
	var next fileToolState
	var eventIndexValue uint64
	hasEvent := false
	switch variant.Update.Kind {
	case "tool_call_delta_chunk":
		if event.Method != "_x.ai/session_notification" {
			return fact, incompatible()
		}
		delta, err := parseFileToolDelta(event.Params, o.session)
		if err != nil {
			return fact, err
		}
		if delta.Update.ID != nil {
			id = *delta.Update.ID
		} else {
			// Later native argument chunks omit both ID and name. Only the
			// unique still-streaming original index may supply their owner;
			// preserve those missing fields in the returned native fact.
			for candidate, state := range o.tools {
				if state.phase == "" && state.index == delta.Update.Index {
					if id != "" {
						return fact, incompatible()
					}
					id = candidate
				}
			}
			if id == "" {
				return fact, incompatible()
			}
		}
		prior, exists := o.tools[id]
		if exists && (prior.phase != "" || delta.Update.Name != nil && prior.name != *delta.Update.Name || prior.index != delta.Update.Index) || len(prior.arguments)+len(delta.Update.Arguments) > 256<<10 {
			return fact, incompatible()
		}
		next = prior
		if !exists {
			for _, state := range o.tools {
				if state.phase == "" && state.index == delta.Update.Index {
					return fact, incompatible()
				}
			}
			next.name, next.index = *delta.Update.Name, delta.Update.Index
		}
		next.arguments += delta.Update.Arguments
		fact.Delta = &delta
	case "tool_call", "tool_call_update":
		if event.Method != "session/update" {
			return fact, incompatible()
		}
		id = variant.Update.ID
		prior, exists := o.tools[id]
		if !exists {
			return fact, incompatible()
		}
		observed, err := parseFileToolObservation(event.Params, o.session, o.prompt, &prior.input)
		if err != nil {
			return fact, err
		}
		next = prior
		switch observed.Phase {
		case fileToolDeclared:
			input, err := parseFileToolInput([]byte(prior.arguments), prior.name, false)
			if prior.phase != "" || err != nil || input != observed.Input {
				return fact, incompatible()
			}
			next.input, next.arguments = observed.Input, ""
			if observed.Input.Name == writeFileTool {
				if o.plans != nil && o.plans.mode == NativePlanMode {
					next.plan, err = o.plans.fileOrigin(observed.Input)
					if err != nil {
						return fact, err
					}
				} else {
					next.inherited = o.editPolicy
				}
			}
		case fileToolDescribed:
			if prior.phase != fileToolDeclared || prior.resolved || prior.plan == nil && !prior.pending || prior.plan != nil && prior.pending {
				return fact, incompatible()
			}
			next.details = historyValueDigest(permissionTool(observed))
		case fileToolCompleted, fileToolFailed:
			if prior.phase != fileToolDescribed {
				return fact, incompatible()
			}
			if prior.plan != nil {
				if prior.pending || prior.resolved || prior.permission != "" || prior.inherited != "" || observed.Phase != fileToolCompleted {
					return fact, incompatible()
				}
			} else if !prior.pending || !prior.resolved || prior.name == writeFileTool && prior.permission == "" && (prior.inherited == "" || prior.inherited != o.editPolicy || observed.Phase != fileToolCompleted) {
				return fact, incompatible()
			}
		default:
			return fact, incompatible()
		}
		eventIndexValue, err = eventIndex(observed.Meta.Event, o.session)
		if err != nil || o.seenEvent && eventIndexValue <= o.lastEvent {
			return fact, incompatible()
		}
		hasEvent = true
		next.phase = observed.Phase
		fact.Observation = &observed
	case string(fileInteractionPending), string(fileInteractionResolved):
		if event.Method != "_x.ai/session_notification" {
			return fact, incompatible()
		}
		interaction, err := parseFileInteraction(event.Params, o.session)
		if err != nil {
			return fact, err
		}
		id = interaction.ID
		prior, exists := o.tools[id]
		if !exists || prior.plan != nil {
			return fact, incompatible()
		}
		next = prior
		if interaction.Kind == fileInteractionPending {
			if prior.phase != fileToolDeclared || prior.pending {
				return fact, incompatible()
			}
			next.pending = true
		} else {
			if prior.phase != fileToolDescribed || !prior.pending || prior.resolved {
				return fact, incompatible()
			}
			next.resolved = true
		}
		fact.Interaction = &interaction
	default:
		return fact, incompatible()
	}
	if _, exists := o.tools[id]; !exists && len(o.tools) >= 128 {
		return fileToolFact{}, domain.Fail(domain.ResourceExhausted, "Native file-tool count reached its bound.", "Retain the original input and reconcile its tools without replay.")
	}
	if next.plan != nil && fact.Observation != nil && fact.Observation.Phase == fileToolCompleted {
		if err := o.plans.commitWrite(id, *next.plan, fact.Observation.Write); err != nil {
			return fileToolFact{}, err
		}
	}
	o.tools[id] = next
	fact.InheritedPermission = next.inherited
	if next.plan != nil {
		origin := *next.plan
		fact.PlanFile = &origin
	}
	o.bytes += len(event.Params)
	if hasEvent {
		o.lastEvent, o.seenEvent = eventIndexValue, true
	}
	return fact, nil
}

// The live original controller is the sole caller. Read-only inspection cannot
// reconstruct this policy from a journal or from an unrequested resolution.
func (o *fileToolObserver) observeEditPolicy(arrival domain.ID) error {
	if o == nil || arrival.Validate() != nil || o.editPolicy != "" && o.editPolicy != arrival {
		return incompatible()
	}
	o.editPolicy = arrival
	return nil
}

func (o *fileToolObserver) settled() bool {
	if o == nil || len(o.tools) == 0 {
		return false
	}
	for _, tool := range o.tools {
		if tool.phase != fileToolCompleted || !tool.resolved {
			return false
		}
	}
	return true
}
