// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// No native process, environment lookup, trust file or authentication file is
// used. Every read is confined to one explicitly selected directory descriptor.
func inspectNativeConfiguration(ctx context.Context, selection domain.NativeConfigurationPreviewSelection) (domain.NativeConfigurationObservation, error) {
	result := domain.NativeConfigurationObservation{Version: 1, Sources: []domain.NativeConfigurationSource{}, Roots: []string{}, Entries: []domain.NativeConfigurationEntry{}}
	if err := selection.Validate(); err != nil {
		return result, err
	}
	total, projectBytes := 0, 0
	lastProject := ""
	for index, scope := range selection.Scopes {
		if ctx.Err() != nil {
			return result, domain.Fail(domain.Canceled, "Native configuration inspection was canceled.", "Create a new preview if needed.")
		}
		if !filepath.IsAbs(scope.Path) || filepath.Clean(scope.Path) != scope.Path {
			return result, domain.NativeConfigurationInvalid()
		}
		canonical, err := filepath.EvalSymlinks(scope.Path)
		if err != nil || canonical != scope.Path {
			return result, domain.NativeConfigurationInvalid()
		}
		if scope.Kind == domain.NativeConfigurationProject {
			if lastProject != "" {
				rel, err := filepath.Rel(lastProject, scope.Path)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return result, domain.NativeConfigurationInvalid()
				}
			}
			lastProject = scope.Path
		}
		root, err := os.OpenRoot(scope.Path)
		if err != nil {
			return result, nativeConfigurationReadFailure()
		}
		rootInfo, err := root.Stat(".")
		if err != nil {
			root.Close()
			return result, nativeConfigurationReadFailure()
		}
		result.Roots = append(result.Roots, nativeConfigurationRootDigest(scope.Path, rootInfo))
		files := domain.NativeConfigurationScopeFiles(scope)
		values := make([][]byte, len(files))
		present := make([]bool, len(files))
		modes := make([]bool, len(files))
		for n, name := range files {
			// Home uses the first nonempty candidate. Project uses the first existing
			// regular candidate; a blank project override masks the default file.
			if n == 2 && present[1] && (scope.Kind == domain.NativeConfigurationProject || len(strings.TrimSpace(string(values[1]))) > 0) {
				result.Sources = append(result.Sources, domain.NativeConfigurationSource{ScopeIndex: uint32(index), File: name, Digest: domain.HarnessEvidenceDigest(nil), Shadowed: true})
				continue
			}
			raw, exists, err := readNativeConfigurationFile(root, name)
			if err != nil {
				root.Close()
				return result, err
			}
			total += len(raw)
			if total > domain.MaxNativeConfigurationSourceBytes {
				root.Close()
				return result, nativeConfigurationReadFailure()
			}
			values[n], present[n] = raw, exists
			if exists {
				info, err := root.Lstat(name)
				if err != nil {
					root.Close()
					return result, nativeConfigurationReadFailure()
				}
				modes[n] = info.Mode()&0111 != 0
			}
			result.Sources = append(result.Sources, domain.NativeConfigurationSource{ScopeIndex: uint32(index), File: name, Present: exists, Digest: domain.HarnessEvidenceDigest(raw)})
		}
		pathInfo, pathErr := os.Stat(scope.Path)
		if pathErr != nil || !os.SameFile(rootInfo, pathInfo) {
			root.Close()
			return result, nativeConfigurationReadFailure()
		}
		root.Close()
		resource := func(n int) domain.NativeConfigurationPackageFile {
			return domain.NativeConfigurationPackageFile{ManagedSkillResource: domain.ManagedSkillResource{Path: files[n], Bytes: len(values[n]), Digest: domain.HarnessEvidenceDigest(values[n]), Executable: modes[n]}, Contents: string(values[n])}
		}
		excluded := func(n int) {
			appendNativeConfigurationEntry(&result, domain.NativeConfigurationEntry{ScopeIndex: uint32(index), File: files[n], SourceDigest: domain.HarnessEvidenceDigest(values[n]), Kind: domain.NativeConfigurationExcluded, Name: "excluded", Reason: domain.NativeReasonSecret})
		}
		if len(values[0]) > 0 {
			// Exclude the entire source before parsing or emitting any candidate bytes.
			if domain.NativeConfigurationSecret(string(values[0])) {
				excluded(0)
			} else {
				var config map[string]any
				if toml.Unmarshal(values[0], &config) != nil {
					return result, nativeConfigurationReadFailure()
				}
				start := len(result.Entries)
				if err := appendNativeConfigurationTable(&result, uint32(index), files[0], domain.HarnessEvidenceDigest(values[0]), "", config); err != nil {
					return result, err
				}
				if scope.Profile != "" {
					profiles, ok := config["profiles"].(map[string]any)
					if !ok {
						return result, domain.NativeConfigurationInvalid()
					}
					profile, ok := profiles[scope.Profile].(map[string]any)
					if !ok {
						return result, domain.NativeConfigurationInvalid()
					}
					if err := appendNativeConfigurationTable(&result, uint32(index), files[0], domain.HarnessEvidenceDigest(values[0]), scope.Profile, profile); err != nil {
						return result, err
					}
				}
				for n := start; n < len(result.Entries); n++ {
					if result.Entries[n].Kind == domain.NativeConfigurationInertExtension {
						entry := &result.Entries[n]
						definition, err := nativeConfigurationDefinition(values[0], entry.Name, entry.Profile)
						if err != nil {
							return result, err
						}
						entry.Files = []domain.NativeConfigurationPackageFile{{ManagedSkillResource: domain.ManagedSkillResource{Path: files[0], Bytes: len(definition), Digest: domain.HarnessEvidenceDigest(definition), Executable: modes[0]}, Contents: string(definition)}}
					}
				}
			}
		} else if scope.Profile != "" {
			return result, domain.NativeConfigurationInvalid()
		}
		for n := 1; n < 3; n++ {
			if len(strings.TrimSpace(string(values[n]))) == 0 {
				continue
			}
			if domain.NativeConfigurationSecret(string(values[n])) {
				excluded(n)
				break
			}
			if scope.Kind == domain.NativeConfigurationProject {
				projectBytes += len(values[n])
				if projectBytes > 32<<10 {
					return result, nativeConfigurationReadFailure()
				}
			}
			appendNativeConfigurationEntry(&result, domain.NativeConfigurationEntry{ScopeIndex: uint32(index), File: files[n], Kind: domain.NativeConfigurationInstruction, Name: "instructions", Contents: string(values[n]), SourceDigest: domain.HarnessEvidenceDigest(values[n]), Files: []domain.NativeConfigurationPackageFile{resource(n)}})
			break
		}
		offset := 3
		for _, pkg := range scope.Packages {
			entry := domain.NativeConfigurationEntry{ScopeIndex: uint32(index), File: files[offset], Name: string(pkg.Kind), Kind: domain.NativeConfigurationInertExtension, SourceDigest: domain.HarnessEvidenceDigest(values[offset]), Files: []domain.NativeConfigurationPackageFile{}}
			if pkg.Kind == domain.NativePackageInstruction {
				entry.Kind = domain.NativeConfigurationInstruction
				entry.Name = "instructions"
				if len(pkg.Files) != 1 {
					return result, domain.NativeConfigurationInvalid()
				}
				entry.Contents = string(values[offset])
			}
			secret := false
			for n := offset; n < offset+len(pkg.Files); n++ {
				if !present[n] {
					return result, nativeConfigurationReadFailure()
				}
				secret = secret || domain.NativeConfigurationSecret(string(values[n]))
				entry.Files = append(entry.Files, resource(n))
			}
			offset += len(pkg.Files)
			if secret {
				entry.Kind = domain.NativeConfigurationExcluded
				entry.Name = "excluded"
				entry.Reason = domain.NativeReasonSecret
				entry.Files = nil
				entry.Contents = ""
			} else if entry.Kind == domain.NativeConfigurationInertExtension {
				entry.Contents = "Stored disabled"
			}
			if entry.Kind == domain.NativeConfigurationInstruction && scope.Kind == domain.NativeConfigurationProject {
				projectBytes += len(entry.Contents)
				if projectBytes > 32<<10 {
					return result, nativeConfigurationReadFailure()
				}
			}
			appendNativeConfigurationEntry(&result, entry)
		}
	}
	if err := result.Validate(selection); err != nil {
		return result, err
	}
	return result, nil
}
func nativeConfigurationRootDigest(path string, info os.FileInfo) string {
	// Device/inode are stable across reads; atime is deliberately excluded. On
	// platforms without those fields, directory identity metadata also fences drift.
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() == reflect.Struct {
		dev, ino := value.FieldByName("Dev"), value.FieldByName("Ino")
		if dev.IsValid() && ino.IsValid() {
			return domain.HarnessEvidenceDigest([]byte(fmt.Sprintf("%s:%v:%v", path, dev, ino)))
		}
	}
	return domain.HarnessEvidenceDigest([]byte(fmt.Sprintf("%s:%s:%d:%d:%s", path, info.Name(), info.Mode(), info.Size(), info.ModTime().UTC())))
}
func nativeConfigurationReadFailure() error {
	return domain.Fail(domain.InvalidArgument, "A selected native configuration source could not be read safely.", "Select regular UTF-8 files within the explicit directory and keep the complete source within its bound.")
}
func readNativeConfigurationFile(root *os.Root, name string) ([]byte, bool, error) {
	if parent := filepath.Dir(filepath.FromSlash(name)); parent != "." {
		for _, part := range strings.Split(parent, string(filepath.Separator)) {
			_ = part
		}
		// Reject every intermediate link, including links that stay inside the root.
		components := strings.Split(parent, string(filepath.Separator))
		prefix := ""
		for _, component := range components {
			prefix = filepath.Join(prefix, component)
			i, e := root.Lstat(prefix)
			if os.IsNotExist(e) {
				return nil, false, nil
			}
			if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
				return nil, false, nativeConfigurationReadFailure()
			}
		}
		info, err := root.Lstat(parent)
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, false, nativeConfigurationReadFailure()
		}
	}
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > domain.MaxNativeConfigurationFileBytes {
		return nil, false, nativeConfigurationReadFailure()
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, false, nativeConfigurationReadFailure()
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, false, nativeConfigurationReadFailure()
	}
	raw, err := io.ReadAll(io.LimitReader(file, domain.MaxNativeConfigurationFileBytes+1))
	after, statErr := file.Stat()
	pathInfo, pathErr := root.Lstat(name)
	if err != nil || statErr != nil || pathErr != nil || len(raw) > domain.MaxNativeConfigurationFileBytes || !os.SameFile(info, pathInfo) || !pathInfo.Mode().IsRegular() || !utf8.Valid(raw) || after.Mode() != info.Mode() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, false, nativeConfigurationReadFailure()
	}
	return raw, true, nil
}
func appendNativeConfigurationEntry(result *domain.NativeConfigurationObservation, entry domain.NativeConfigurationEntry) {
	proof, _ := json.Marshal(struct {
		Scope   uint32
		File    string
		Profile string
		Kind    domain.NativeConfigurationEntryKind
		Name    string
		Ordinal int
		Digest  string
	}{entry.ScopeIndex, entry.File, entry.Profile, entry.Kind, entry.Name, len(result.Entries), entry.SourceDigest})
	entry.ID = domain.HarnessEvidenceDigest(proof)
	result.Entries = append(result.Entries, entry)
}
func appendNativeConfigurationTable(result *domain.NativeConfigurationObservation, index uint32, file, digest, profile string, table map[string]any) error {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(result.Entries) >= 128 {
			return nativeConfigurationReadFailure()
		}
		entry := domain.NativeConfigurationEntry{Profile: profile, ScopeIndex: index, File: file, SourceDigest: digest, Kind: domain.NativeConfigurationUnsupported, Name: "unsupported", Reason: domain.NativeReasonUnsupported}
		name := map[string]string{"model": "model", "model_reasoning_effort": "effort", "sandbox_mode": "permission", "approval_policy": "approval_policy", "service_tier": "service_tier"}[key]
		if name != "" {
			value, ok := table[key].(string)
			if ok && key == "sandbox_mode" && value == "danger-full-access" {
				value = string(domain.PermissionFullAccess)
			}
			raw, _ := json.Marshal(value)
			if ok {
				if _, err := domain.NativeConfigurationSettingValue(name, raw); err == nil {
					entry.Kind = domain.NativeConfigurationSetting
					entry.Name = name
					entry.Value = raw
				} else if domain.NativeConfigurationSecret(value) {
					entry.Kind = domain.NativeConfigurationExcluded
					entry.Name = "excluded"
				}
			}
		} else if slices.Contains([]string{"hooks", "plugins", "mcp_servers"}, key) {
			// Preserve the exact selected definition as disabled package bytes. Native
			// paths and commands do not expand reads or grant activation authority.
			entry.Kind = domain.NativeConfigurationInertExtension
			entry.Name = key
			definition, err := json.Marshal(table[key])
			if err != nil {
				return nativeConfigurationReadFailure()
			}
			entry.Contents = string(definition)
		} else if domain.NativeConfigurationSensitiveKey(key) || slices.Contains([]string{"auth", "authentication", "managed_requirements", "requirements", "memories", "memory", "forced_login_method", "forced_chatgpt_workspace_id"}, key) {
			entry.Kind = domain.NativeConfigurationExcluded
			entry.Name = "excluded"
		}
		appendNativeConfigurationEntry(result, entry)
	}
	return nil
}
func executeNativeConfigurationImport(ctx context.Context, config Config, job domain.Job) (json.RawMessage, error) {
	var input domain.NativeConfigurationJobInput
	if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Preview.MachineID != job.MachineID || input.InstanceID != job.InstanceID || input.WorkerDeviceID != job.AssignedDeviceID || (job.Type == domain.NativeConfigurationApplyJob) != (input.Selection != nil) {
		return nil, domain.NativeConfigurationInvalid()
	}
	result, err := inspectNativeConfiguration(ctx, input.Preview)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, domain.NativeConfigurationInvalid()
	}
	if input.Selection != nil && domain.HarnessEvidenceDigest(raw) != input.Selection.PreviewDigest {
		return nil, domain.NativeConfigurationChanged()
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "native configuration source inspected", "job_type", job.Type, "machine_id", job.MachineID, "source_count", len(result.Sources), "entry_count", len(result.Entries))
	}
	return raw, nil
}

// Parser expression ranges preserve original selected definition bytes without
// exposing unrelated unsupported values from the same nonsecret config file.
func nativeConfigurationDefinition(raw []byte, name, profile string) ([]byte, error) {
	var parser unstable.Parser
	parser.Reset(raw)
	type expression struct {
		start    int
		selected bool
	}
	var expressions []expression
	var table []string
	prefix := []string{name}
	if profile != "" {
		prefix = []string{"profiles", profile, name}
	}
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind != unstable.KeyValue && node.Kind != unstable.Table && node.Kind != unstable.ArrayTable {
			continue
		}
		it := node.Key()
		var keys []string
		start := -1
		for it.Next() {
			key := it.Node()
			keys = append(keys, string(key.Data))
			if start < 0 {
				start = int(key.Raw.Offset)
			}
		}
		if start < 0 {
			return nil, nativeConfigurationReadFailure()
		}
		for start > 0 && raw[start-1] != '\n' {
			start--
		}
		full := keys
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			table = keys
		} else {
			full = append(slices.Clone(table), keys...)
		}
		selected := len(full) >= len(prefix)
		for i := 0; selected && i < len(prefix); i++ {
			selected = full[i] == prefix[i]
		}
		expressions = append(expressions, expression{start, selected})
	}
	if parser.Error() != nil {
		return nil, nativeConfigurationReadFailure()
	}
	var result []byte
	for i, e := range expressions {
		if !e.selected {
			continue
		}
		end := len(raw)
		if i+1 < len(expressions) {
			end = expressions[i+1].start
		}
		result = append(result, raw[e.start:end]...)
	}
	if len(result) == 0 {
		return nil, nativeConfigurationReadFailure()
	}
	return result, nil
}
