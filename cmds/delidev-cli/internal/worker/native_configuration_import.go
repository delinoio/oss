// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/pelletier/go-toml/v2"
)

// No native process, environment lookup, trust file or authentication file is
// used. Every read is confined to one explicitly selected directory descriptor.
func inspectNativeConfiguration(ctx context.Context, selection domain.NativeConfigurationPreviewSelection) (domain.NativeConfigurationObservation, error) {
	if selection.Harness == domain.ClaudeCode {
		return inspectClaudeConfiguration(ctx, selection)
	}
	result := domain.NativeConfigurationObservation{Version: 1, Sources: []domain.NativeConfigurationSource{}, Entries: []domain.NativeConfigurationEntry{}}
	if err := selection.Validate(); err != nil {
		return result, err
	}
	total := 0
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
		files := domain.NativeConfigurationFiles(scope.Kind)
		values := make([][]byte, len(files))
		for n, name := range files {
			if n == 2 && len(strings.TrimSpace(string(values[1]))) > 0 {
				result.Sources = append(result.Sources, domain.NativeConfigurationSource{ScopeIndex: uint32(index), File: name, Digest: domain.HarnessEvidenceDigest(nil), Shadowed: true})
				continue
			}
			raw, present, err := readNativeConfigurationFile(root, name)
			if err != nil {
				root.Close()
				return result, err
			}
			total += len(raw)
			if total > domain.MaxNativeConfigurationSourceBytes {
				root.Close()
				return result, nativeConfigurationReadFailure()
			}
			values[n] = raw
			result.Sources = append(result.Sources, domain.NativeConfigurationSource{ScopeIndex: uint32(index), File: name, Present: present, Digest: domain.HarnessEvidenceDigest(raw)})
		}
		root.Close()
		if len(values[0]) > 0 {
			var config map[string]any
			if !utf8.Valid(values[0]) || toml.Unmarshal(values[0], &config) != nil {
				return result, nativeConfigurationReadFailure()
			}
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
		} else if scope.Profile != "" {
			return result, domain.NativeConfigurationInvalid()
		}
		// Native precedence chooses the first nonempty override/default file. Keep
		// override evidence so newly added or edited overrides invalidate apply. A
		// masked default file is never read and carries an explicit shadowed marker.
		for n := 1; n < len(files); n++ {
			if len(strings.TrimSpace(string(values[n]))) == 0 {
				continue
			}
			entry := domain.NativeConfigurationEntry{ScopeIndex: uint32(index), File: files[n], Kind: domain.NativeConfigurationInstruction, Name: "instructions", Contents: string(values[n]), SourceDigest: domain.HarnessEvidenceDigest(values[n])}
			if !utf8.Valid(values[n]) || domain.NativeConfigurationSecret(entry.Contents) {
				entry.Kind = domain.NativeConfigurationExcluded
				entry.Name = "excluded"
				entry.Contents = ""
			}
			appendNativeConfigurationEntry(&result, entry)
			break
		}
	}
	if err := result.Validate(selection); err != nil {
		return result, err
	}
	return result, nil
}
func nativeConfigurationReadFailure() error {
	return domain.Fail(domain.InvalidArgument, "A selected native configuration source could not be read safely.", "Select regular UTF-8 files within the explicit directory and keep the complete source within its bound.")
}
func readNativeConfigurationFile(root *os.Root, name string) ([]byte, bool, error) {
	if parent := filepath.Dir(filepath.FromSlash(name)); parent != "." {
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
	if err != nil || statErr != nil || pathErr != nil || len(raw) > domain.MaxNativeConfigurationFileBytes || !os.SameFile(info, pathInfo) || !pathInfo.Mode().IsRegular() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
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
		entry := domain.NativeConfigurationEntry{Profile: profile, ScopeIndex: index, File: file, SourceDigest: digest, Kind: domain.NativeConfigurationUnsupported, Name: "unsupported"}
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
			// Import only an inert category descriptor. Commands, URLs, environment,
			// server identities, plugin paths and trust flags remain Worker-local.
			entry.Kind = domain.NativeConfigurationInertExtension
			entry.Name = key
			entry.Contents = `{"execution_authority":false,"credential_authority":false,"trust":false}`
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
