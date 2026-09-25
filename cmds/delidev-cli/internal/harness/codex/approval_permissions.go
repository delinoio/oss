package codex

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type FilePermissionAccess string
type FilePermissionPathKind string
type FilePermissionSpecialKind string

const (
	FilePermissionRead     FilePermissionAccess      = "read"
	FilePermissionWrite    FilePermissionAccess      = "write"
	FilePermissionDeny     FilePermissionAccess      = "deny"
	FilePermissionPath     FilePermissionPathKind    = "path"
	FilePermissionGlob     FilePermissionPathKind    = "glob_pattern"
	FilePermissionSpecial  FilePermissionPathKind    = "special"
	FilePermissionRoot     FilePermissionSpecialKind = "root"
	FilePermissionMinimal  FilePermissionSpecialKind = "minimal"
	FilePermissionProjects FilePermissionSpecialKind = "project_roots"
	FilePermissionTmpdir   FilePermissionSpecialKind = "tmpdir"
	FilePermissionSlashTmp FilePermissionSpecialKind = "slash_tmp"
	FilePermissionUnknown  FilePermissionSpecialKind = "unknown"
)

// These are the pinned native permission descriptors. Paths remain exact
// strings; decoding never resolves them on the server, expands globs or creates
// a synthetic common sandbox across native harnesses.
type PermissionProfile struct {
	Network    *AdditionalNetworkPermissions `json:"network,omitempty"`
	FileSystem *AdditionalFilePermissions    `json:"fileSystem,omitempty"`
}
type AdditionalNetworkPermissions struct {
	Enabled *bool `json:"enabled"`
}
type AdditionalFilePermissions struct {
	Read             []string              `json:"read"`
	Write            []string              `json:"write"`
	Entries          []FilePermissionEntry `json:"entries"`
	GlobScanMaxDepth *uint32               `json:"globScanMaxDepth"`
}
type FilePermissionEntry struct {
	Access FilePermissionAccess    `json:"access"`
	Path   FilePermissionPathValue `json:"path"`
}
type FilePermissionPathValue struct {
	Kind    FilePermissionPathKind      `json:"type"`
	Path    *string                     `json:"path,omitempty"`
	Pattern *string                     `json:"pattern,omitempty"`
	Special *FilePermissionSpecialValue `json:"value,omitempty"`
}
type FilePermissionSpecialValue struct {
	Kind    FilePermissionSpecialKind `json:"kind"`
	Path    *string                   `json:"path,omitempty"`
	Subpath *string                   `json:"subpath,omitempty"`
}

func (p *PermissionProfile) UnmarshalJSON(raw []byte) error {
	type plain PermissionProfile
	var decoded plain
	if domain.Decode(raw, &decoded) != nil {
		return incompatible()
	}
	*p = PermissionProfile(decoded)
	return p.Validate()
}
func (p PermissionProfile) Validate() error {
	if p.FileSystem == nil {
		return nil
	}
	f := p.FileSystem
	if !validApprovalStrings(f.Read, false) || !validApprovalStrings(f.Write, false) || len(f.Entries) > 1024 || (f.GlobScanMaxDepth != nil && *f.GlobScanMaxDepth == 0) {
		return incompatible()
	}
	for _, entry := range f.Entries {
		if !slices.Contains([]FilePermissionAccess{FilePermissionRead, FilePermissionWrite, FilePermissionDeny}, entry.Access) || entry.Path.validate() != nil {
			return incompatible()
		}
	}
	return nil
}

func (p *FilePermissionPathValue) UnmarshalJSON(raw []byte) error {
	type plain FilePermissionPathValue
	var decoded plain
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &decoded) != nil || domain.Decode(raw, &fields) != nil {
		return incompatible()
	}
	field := map[FilePermissionPathKind]string{FilePermissionPath: "path", FilePermissionGlob: "pattern", FilePermissionSpecial: "value"}[decoded.Kind]
	if len(fields) != 2 || fields["type"] == nil || fields[field] == nil {
		return incompatible()
	}
	*p = FilePermissionPathValue(decoded)
	return p.validate()
}

func (s *FilePermissionSpecialValue) UnmarshalJSON(raw []byte) error {
	type plain FilePermissionSpecialValue
	var decoded plain
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &decoded) != nil || domain.Decode(raw, &fields) != nil {
		return incompatible()
	}
	for field := range fields {
		if field == "kind" || (field == "path" && decoded.Kind == FilePermissionUnknown) || (field == "subpath" && (decoded.Kind == FilePermissionUnknown || decoded.Kind == FilePermissionProjects)) {
			continue
		}
		return incompatible()
	}
	*s = FilePermissionSpecialValue(decoded)
	return (FilePermissionPathValue{Kind: FilePermissionSpecial, Special: s}).validate()
}
func (p FilePermissionPathValue) validate() error {
	valid := func(v *string) bool {
		return v != nil && domain.Text(*v, "native filesystem approval path", 4096, true) == nil
	}
	switch p.Kind {
	case FilePermissionPath:
		if !valid(p.Path) || p.Pattern != nil || p.Special != nil {
			return incompatible()
		}
	case FilePermissionGlob:
		if !valid(p.Pattern) || p.Path != nil || p.Special != nil {
			return incompatible()
		}
	case FilePermissionSpecial:
		if p.Path != nil || p.Pattern != nil || p.Special == nil {
			return incompatible()
		}
		s := p.Special
		if s.Subpath != nil && !valid(s.Subpath) {
			return incompatible()
		}
		switch s.Kind {
		case FilePermissionRoot, FilePermissionMinimal, FilePermissionTmpdir, FilePermissionSlashTmp:
			if s.Path != nil || s.Subpath != nil {
				return incompatible()
			}
		case FilePermissionProjects:
			if s.Path != nil {
				return incompatible()
			}
		case FilePermissionUnknown:
			if !valid(s.Path) {
				return incompatible()
			}
		default:
			return incompatible()
		}
	default:
		return incompatible()
	}
	return nil
}

// PermissionGrant explicitly selects scope. Only an exact requested descriptor
// (or a write entry reduced to read access) may be granted. This comparison does
// not claim to resolve native paths or prove resulting sandbox enforcement.
type PermissionGrant struct {
	Permissions      PermissionProfile    `json:"permissions"`
	Scope            PermissionGrantScope `json:"scope"`
	StrictAutoReview *bool                `json:"strictAutoReview,omitempty"`
}

func (g PermissionGrant) validate(request *PermissionsApprovalRequest) error {
	invalid := func() error {
		return domain.Fail(domain.InvalidArgument, "The grant exceeds the original native permission request.", "Choose only requested access and an explicit native scope; do not introduce new paths, rules or network access.")
	}
	if request == nil || request.Permissions.Validate() != nil || g.Permissions.Validate() != nil || (g.Scope != PermissionTurn && g.Scope != PermissionSession) || (g.StrictAutoReview != nil && *g.StrictAutoReview && g.Scope != PermissionTurn) {
		return invalid()
	}
	want, have := g.Permissions, request.Permissions
	if want.Network != nil && want.Network.Enabled != nil && *want.Network.Enabled && (have.Network == nil || have.Network.Enabled == nil || !*have.Network.Enabled) {
		return invalid()
	}
	if want.FileSystem == nil {
		return nil
	}
	f, r := want.FileSystem, have.FileSystem
	if r == nil {
		if len(f.Read) != 0 || len(f.Write) != 0 || len(f.Entries) != 0 || f.GlobScanMaxDepth != nil {
			return invalid()
		}
		return nil
	}
	granted, requested := effectivePermissionEntries(f), effectivePermissionEntries(r)
	for _, entry := range granted {
		if !slices.ContainsFunc(requested, func(original FilePermissionEntry) bool {
			return reflect.DeepEqual(original.Path, entry.Path) && (original.Access == entry.Access || (original.Access == FilePermissionWrite && entry.Access == FilePermissionRead))
		}) {
			return invalid()
		}
	}
	if len(granted) != 0 {
		// Retain effective deny descriptors while granting any filesystem access.
		// Native entries take precedence over their deprecated read/write mirrors.
		for _, entry := range requested {
			if entry.Access == FilePermissionDeny && !slices.ContainsFunc(granted, func(selected FilePermissionEntry) bool { return reflect.DeepEqual(entry, selected) }) {
				return invalid()
			}
		}
	}
	if f.GlobScanMaxDepth != nil && (r.GlobScanMaxDepth == nil || *f.GlobScanMaxDepth != *r.GlobScanMaxDepth) {
		return invalid()
	}
	return nil
}

// The pinned app-server conversion treats a present entries collection,
// including an empty one, as authoritative over legacy read/write mirrors.
// Preserve descriptor order and bytes; this does not interpret filesystem paths.
func effectivePermissionEntries(fs *AdditionalFilePermissions) []FilePermissionEntry {
	if fs == nil {
		return nil
	}
	if fs.Entries != nil {
		return slices.Clone(fs.Entries)
	}
	result := make([]FilePermissionEntry, 0, len(fs.Read)+len(fs.Write))
	for _, path := range fs.Read {
		result = append(result, FilePermissionEntry{Access: FilePermissionRead, Path: FilePermissionPathValue{Kind: FilePermissionPath, Path: &path}})
	}
	for _, path := range fs.Write {
		result = append(result, FilePermissionEntry{Access: FilePermissionWrite, Path: FilePermissionPathValue{Kind: FilePermissionPath, Path: &path}})
	}
	return result
}
