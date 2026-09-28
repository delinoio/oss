package domain

import "slices"

type OpenCodeWorkspaceEventKind string

const (
	OpenCodeFileEdited   OpenCodeWorkspaceEventKind = "file-edited"
	OpenCodeFileAdded    OpenCodeWorkspaceEventKind = "file-added"
	OpenCodeFileChanged  OpenCodeWorkspaceEventKind = "file-changed"
	OpenCodeFileUnlinked OpenCodeWorkspaceEventKind = "file-unlinked"
)

// Native file notifications are instance observations without a native session,
// message, or tool owner. Preserve that boundary instead of assigning causality.
type OpenCodeWorkspaceEvent struct {
	Kind          OpenCodeWorkspaceEventKind `json:"kind"`
	NativeEventID string                     `json:"native_event_id"`
	File          string                     `json:"file"`
}

func (e OpenCodeWorkspaceEvent) Validate() error {
	if !slices.Contains([]OpenCodeWorkspaceEventKind{OpenCodeFileEdited, OpenCodeFileAdded, OpenCodeFileChanged, OpenCodeFileUnlinked}, e.Kind) || NativeIdentity(e.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || Text(e.File, "native workspace event path", 32768, true) != nil {
		return invalidArtifact()
	}
	return nil
}
