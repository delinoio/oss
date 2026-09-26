package domain

import "slices"

type OpenCodeRevisionSource string

const (
	OpenCodeSnapshotRevision   OpenCodeRevisionSource = "snapshot"
	OpenCodePatchRevision      OpenCodeRevisionSource = "patch"
	OpenCodeStepStartRevision  OpenCodeRevisionSource = "step-start"
	OpenCodeStepFinishRevision OpenCodeRevisionSource = "step-finish"
)

// Native revision references are opaque observations, not DeliDev Git objects,
// restore checkpoints, file-reading capabilities, or authority to apply a patch.
type OpenCodeRevision struct {
	Source OpenCodeRevisionSource `json:"source"`
	Hash   string                 `json:"hash"`
	Files  []string               `json:"files"`
}

func (r OpenCodeRevision) Validate() error {
	if !slices.Contains([]OpenCodeRevisionSource{OpenCodeSnapshotRevision, OpenCodePatchRevision, OpenCodeStepStartRevision, OpenCodeStepFinishRevision}, r.Source) || Text(r.Hash, "native revision reference", 4096, true) != nil {
		return invalidArtifact()
	}
	if r.Source == OpenCodePatchRevision {
		if r.Files == nil || len(r.Files) > 4096 {
			return invalidArtifact()
		}
		for _, p := range r.Files {
			if Text(p, "native revision path", 32768, true) != nil {
				return invalidArtifact()
			}
		}
	} else if r.Files != nil {
		return invalidArtifact()
	}
	return nil
}

type OpenCodeDiffStatus string

const (
	OpenCodeDiffAdded    OpenCodeDiffStatus = "added"
	OpenCodeDiffDeleted  OpenCodeDiffStatus = "deleted"
	OpenCodeDiffModified OpenCodeDiffStatus = "modified"
)

type OpenCodeFileDiff struct {
	File      *string             `json:"file,omitempty"`
	Patch     *string             `json:"patch,omitempty"`
	Additions uint64              `json:"additions"`
	Deletions uint64              `json:"deletions"`
	Status    *OpenCodeDiffStatus `json:"status,omitempty"`
}

func (d *OpenCodeFileDiff) UnmarshalJSON(raw []byte) error {
	var wire struct {
		File      *string             `json:"file,omitempty"`
		Patch     *string             `json:"patch,omitempty"`
		Additions *uint64             `json:"additions"`
		Deletions *uint64             `json:"deletions"`
		Status    *OpenCodeDiffStatus `json:"status,omitempty"`
	}
	if Decode(raw, &wire) != nil || wire.Additions == nil || wire.Deletions == nil {
		return invalidArtifact()
	}
	*d = OpenCodeFileDiff{File: wire.File, Patch: wire.Patch, Additions: *wire.Additions, Deletions: *wire.Deletions, Status: wire.Status}
	return nil
}

type OpenCodeChangeSource string

const (
	OpenCodeSessionDiff  OpenCodeChangeSource = "session-diff"
	OpenCodeInputSummary OpenCodeChangeSource = "input-summary"
)

type OpenCodeChanges struct {
	Source          OpenCodeChangeSource `json:"source"`
	NativeEventID   string               `json:"native_event_id"`
	NativeMessageID string               `json:"native_message_id,omitempty"`
	Title           *string              `json:"title,omitempty"`
	Body            *string              `json:"body,omitempty"`
	Diffs           []OpenCodeFileDiff   `json:"diffs"`
}

func (c OpenCodeChanges) Validate() error {
	if NativeIdentity(c.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || c.Diffs == nil || len(c.Diffs) > 4096 {
		return invalidArtifact()
	}
	switch c.Source {
	case OpenCodeSessionDiff:
		if c.NativeMessageID != "" || c.Title != nil || c.Body != nil {
			return invalidArtifact()
		}
	case OpenCodeInputSummary:
		if NativeIdentity(c.NativeMessageID).Validate(OpenCode, NativeMessageIdentity) != nil {
			return invalidArtifact()
		}
	default:
		return invalidArtifact()
	}
	for _, s := range []*string{c.Title, c.Body} {
		if s != nil && Text(*s, "native change summary", MaxMessageText, false) != nil {
			return invalidArtifact()
		}
	}
	for _, d := range c.Diffs {
		if d.Additions > maxNativeExactInteger || d.Deletions > maxNativeExactInteger || d.Status != nil && !slices.Contains([]OpenCodeDiffStatus{OpenCodeDiffAdded, OpenCodeDiffDeleted, OpenCodeDiffModified}, *d.Status) {
			return invalidArtifact()
		}
		for _, s := range []*string{d.File, d.Patch} {
			if s != nil && Text(*s, "native file diff", MaxMessageText, false) != nil {
				return invalidArtifact()
			}
		}
	}
	return nil
}
