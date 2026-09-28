package grok

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type fileInteractionKind string
type filePermissionKind string

const (
	fileInteractionPending  fileInteractionKind = "pending_interaction"
	fileInteractionResolved fileInteractionKind = "interaction_resolved"
	fileAllowOnce           filePermissionKind  = "allow_once"
	fileAllowSession        filePermissionKind  = "allow_always"
	fileRejectOnce          filePermissionKind  = "reject_once"
)

type fileInteraction struct {
	Kind fileInteractionKind
	ID   string
}

type filePermissionOption struct {
	ID   string             `json:"optionId"`
	Name string             `json:"name"`
	Kind filePermissionKind `json:"kind"`
}

type filePermissionTool struct {
	ID         string
	Input      fileToolInput
	Title      string
	Descriptor toolDescriptor
}

type filePermission struct {
	Tool    filePermissionTool
	Options []filePermissionOption
}

func parseFileInteraction(raw []byte, session domain.ID) (fileInteraction, error) {
	var value struct {
		Session domain.ID `json:"sessionId"`
		Update  struct {
			Kind fileInteractionKind `json:"sessionUpdate"`
			ID   string              `json:"tool_call_id"`
			Type *string             `json:"kind,omitempty"`
		} `json:"update"`
	}
	if session.Validate() != nil || decode(raw, &value) != nil || value.Session != session || !text(value.Update.ID, 256) {
		return fileInteraction{}, incompatible()
	}
	switch value.Update.Kind {
	case fileInteractionPending:
		if value.Update.Type == nil || *value.Update.Type != "permission" {
			return fileInteraction{}, incompatible()
		}
	case fileInteractionResolved:
		if value.Update.Type != nil {
			return fileInteraction{}, incompatible()
		}
	default:
		return fileInteraction{}, incompatible()
	}
	return fileInteraction{Kind: value.Update.Kind, ID: value.Update.ID}, nil
}

func permissionTool(observed fileToolObservation) filePermissionTool {
	return filePermissionTool{ID: observed.ID, Input: observed.Input, Title: observed.Title, Descriptor: observed.Descriptor}
}

// A matching proposal carries no answer or native acceptance. Even a later
// interaction_resolved notification does not identify a user's decision.
func parseFilePermission(raw []byte, session domain.ID) (filePermission, error) {
	var value filePermission
	var request struct {
		Session domain.ID `json:"sessionId"`
		Tool    struct {
			ID       string                 `json:"toolCallId"`
			Category fileToolKind           `json:"kind"`
			Title    string                 `json:"title"`
			Input    json.RawMessage        `json:"rawInput"`
			Meta     toolDescriptorEnvelope `json:"_meta"`
		} `json:"toolCall"`
		Options []filePermissionOption `json:"options"`
	}
	if len(raw) > 1<<20 || session.Validate() != nil || decode(raw, &request) != nil || request.Session != session || !text(request.Tool.ID, 256) || request.Tool.Category != fileEditKind || !text(request.Tool.Title, 16<<10) || request.Tool.Meta.Tool.Name != writeFileTool || len(request.Options) != 3 {
		return value, incompatible()
	}
	input, err := parseFileToolInput(request.Tool.Input, writeFileTool, true)
	if err != nil || !request.Tool.Meta.Tool.validate(input, true) {
		return value, incompatible()
	}
	ids := []string{"allow-edits-session", "allow-once", "reject-once"}
	kinds := []filePermissionKind{fileAllowSession, fileAllowOnce, fileRejectOnce}
	for index, option := range request.Options {
		if option.ID != ids[index] || option.Kind != kinds[index] || !text(option.Name, 4096) {
			return value, incompatible()
		}
	}
	value.Tool = filePermissionTool{ID: request.Tool.ID, Input: input, Title: request.Tool.Title, Descriptor: request.Tool.Meta.Tool}
	value.Options = request.Options
	return value, nil
}
