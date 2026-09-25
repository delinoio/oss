package domain

import (
	"encoding/json"
	"slices"
)

type CodexFilePermissionAccess string
type CodexFilePermissionPathKind string
type CodexFilePermissionSpecialKind string

const (
	CodexFilePermissionRead     CodexFilePermissionAccess      = "read"
	CodexFilePermissionWrite    CodexFilePermissionAccess      = "write"
	CodexFilePermissionDeny     CodexFilePermissionAccess      = "deny"
	CodexFilePermissionPath     CodexFilePermissionPathKind    = "path"
	CodexFilePermissionGlob     CodexFilePermissionPathKind    = "glob_pattern"
	CodexFilePermissionSpecial  CodexFilePermissionPathKind    = "special"
	CodexFilePermissionRoot     CodexFilePermissionSpecialKind = "root"
	CodexFilePermissionMinimal  CodexFilePermissionSpecialKind = "minimal"
	CodexFilePermissionProjects CodexFilePermissionSpecialKind = "project_roots"
	CodexFilePermissionTmpdir   CodexFilePermissionSpecialKind = "tmpdir"
	CodexFilePermissionSlashTmp CodexFilePermissionSpecialKind = "slash_tmp"
	CodexFilePermissionUnknown  CodexFilePermissionSpecialKind = "unknown"
)

// These are the pinned native permission descriptors. Paths remain exact
// strings; decoding never resolves them on the server, expands globs or creates
// a synthetic common sandbox across native harnesses.
type CodexPermissionProfile struct {
	Network    *CodexAdditionalNetworkPermissions `json:"network,omitempty"`
	FileSystem *CodexAdditionalFilePermissions    `json:"file_system,omitempty"`
}
type CodexAdditionalNetworkPermissions struct {
	Enabled *bool `json:"enabled"`
}
type CodexAdditionalFilePermissions struct {
	Read             []string                   `json:"read"`
	Write            []string                   `json:"write"`
	Entries          []CodexFilePermissionEntry `json:"entries"`
	GlobScanMaxDepth *uint32                    `json:"glob_scan_max_depth"`
}
type CodexFilePermissionEntry struct {
	Access CodexFilePermissionAccess    `json:"access"`
	Path   CodexFilePermissionPathValue `json:"path"`
}
type CodexFilePermissionPathValue struct {
	Kind    CodexFilePermissionPathKind      `json:"type"`
	Path    *string                          `json:"path,omitempty"`
	Pattern *string                          `json:"pattern,omitempty"`
	Special *CodexFilePermissionSpecialValue `json:"value,omitempty"`
}
type CodexFilePermissionSpecialValue struct {
	Kind    CodexFilePermissionSpecialKind `json:"kind"`
	Path    *string                        `json:"path,omitempty"`
	Subpath *string                        `json:"subpath,omitempty"`
}

func (p *CodexPermissionProfile) UnmarshalJSON(raw []byte) error {
	type plain CodexPermissionProfile
	var decoded plain
	if Decode(raw, &decoded) != nil {
		return invalidCodexApproval()
	}
	*p = CodexPermissionProfile(decoded)
	return p.Validate()
}
func (p CodexPermissionProfile) Validate() error {
	if p.FileSystem == nil {
		return nil
	}
	f := p.FileSystem
	if !validCodexApprovalStrings(f.Read, false) || !validCodexApprovalStrings(f.Write, false) || len(f.Entries) > 1024 || (f.GlobScanMaxDepth != nil && *f.GlobScanMaxDepth == 0) {
		return invalidCodexApproval()
	}
	for _, entry := range f.Entries {
		if !slices.Contains([]CodexFilePermissionAccess{CodexFilePermissionRead, CodexFilePermissionWrite, CodexFilePermissionDeny}, entry.Access) || entry.Path.validate() != nil {
			return invalidCodexApproval()
		}
	}
	return nil
}

func (p *CodexFilePermissionPathValue) UnmarshalJSON(raw []byte) error {
	type plain CodexFilePermissionPathValue
	var decoded plain
	var fields map[string]json.RawMessage
	if Decode(raw, &decoded) != nil || Decode(raw, &fields) != nil {
		return invalidCodexApproval()
	}
	field := map[CodexFilePermissionPathKind]string{CodexFilePermissionPath: "path", CodexFilePermissionGlob: "pattern", CodexFilePermissionSpecial: "value"}[decoded.Kind]
	if len(fields) != 2 || fields["type"] == nil || fields[field] == nil {
		return invalidCodexApproval()
	}
	*p = CodexFilePermissionPathValue(decoded)
	return p.validate()
}

func (s *CodexFilePermissionSpecialValue) UnmarshalJSON(raw []byte) error {
	type plain CodexFilePermissionSpecialValue
	var decoded plain
	var fields map[string]json.RawMessage
	if Decode(raw, &decoded) != nil || Decode(raw, &fields) != nil {
		return invalidCodexApproval()
	}
	for field := range fields {
		if field == "kind" || (field == "path" && decoded.Kind == CodexFilePermissionUnknown) || (field == "subpath" && (decoded.Kind == CodexFilePermissionUnknown || decoded.Kind == CodexFilePermissionProjects)) {
			continue
		}
		return invalidCodexApproval()
	}
	*s = CodexFilePermissionSpecialValue(decoded)
	return (CodexFilePermissionPathValue{Kind: CodexFilePermissionSpecial, Special: s}).validate()
}
func (p CodexFilePermissionPathValue) validate() error {
	valid := func(v *string) bool {
		return v != nil && Text(*v, "native filesystem approval path", 4096, true) == nil
	}
	switch p.Kind {
	case CodexFilePermissionPath:
		if !valid(p.Path) || p.Pattern != nil || p.Special != nil {
			return invalidCodexApproval()
		}
	case CodexFilePermissionGlob:
		if !valid(p.Pattern) || p.Path != nil || p.Special != nil {
			return invalidCodexApproval()
		}
	case CodexFilePermissionSpecial:
		if p.Path != nil || p.Pattern != nil || p.Special == nil {
			return invalidCodexApproval()
		}
		s := p.Special
		if s.Subpath != nil && !valid(s.Subpath) {
			return invalidCodexApproval()
		}
		switch s.Kind {
		case CodexFilePermissionRoot, CodexFilePermissionMinimal, CodexFilePermissionTmpdir, CodexFilePermissionSlashTmp:
			if s.Path != nil || s.Subpath != nil {
				return invalidCodexApproval()
			}
		case CodexFilePermissionProjects:
			if s.Path != nil {
				return invalidCodexApproval()
			}
		case CodexFilePermissionUnknown:
			if !valid(s.Path) {
				return invalidCodexApproval()
			}
		default:
			return invalidCodexApproval()
		}
	default:
		return invalidCodexApproval()
	}
	return nil
}
