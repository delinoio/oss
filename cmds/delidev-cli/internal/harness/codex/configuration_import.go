// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/pelletier/go-toml/v2"
)

// ReadConfiguration observes only selected scopes, without executing native
// configuration, following instructions or publishing credentials/trust state.
func ReadConfiguration(ctx context.Context, request domain.NativeConfigurationRead) (domain.NativeConfigurationSnapshot, error) {
	snapshot := domain.NativeConfigurationSnapshot{Version: 1, Scopes: request.Scopes, Files: []domain.NativeConfigurationFile{}, Entries: []domain.NativeConfigurationEntry{}}
	if err := request.Validate(); err != nil {
		return snapshot, err
	}
	total := 0
	for index, scope := range request.Scopes {
		if ctx.Err() != nil {
			return snapshot, domain.SafeError(ctx.Err())
		}
		canonical, err := filepath.EvalSymlinks(scope.Path)
		if err != nil || canonical != scope.Path {
			return snapshot, domain.NativeConfigurationInvalid()
		}
		root, err := os.OpenRoot(scope.Path)
		if err != nil {
			return snapshot, domain.NativeConfigurationInvalid()
		}
		func() {
			defer root.Close()
			config := "config.toml"
			if scope.Kind == domain.NativeConfigurationProject {
				config = filepath.Join(".codex", "config.toml")
			}
			paths := []string{config, "AGENTS.override.md", "AGENTS.md"}
			values := make([][]byte, 3)
			for position, name := range paths {
				file := domain.NativeConfigurationFile{Scope: index, Name: filepath.ToSlash(name)}
				raw, present, e := readConfigurationFile(root, name)
				if e != nil {
					err = e
					return
				}
				total += len(raw)
				if total > domain.MaxNativeConfigurationBytes {
					err = domain.NativeConfigurationInvalid()
					return
				}
				file.Present = present
				file.Usable = position > 0 && len(strings.TrimSpace(string(raw))) > 0
				if present {
					digest := sha256.Sum256(raw)
					file.Digest = hex.EncodeToString(digest[:])
				}
				snapshot.Files = append(snapshot.Files, file)
				values[position] = raw
			}
			if values[0] != nil {
				var configValues map[string]any
				if toml.Unmarshal(values[0], &configValues) != nil {
					err = domain.NativeConfigurationInvalid()
					return
				}
				names := make([]string, 0, len(configValues))
				for name := range configValues {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					entry := domain.NativeConfigurationEntry{Key: fmt.Sprintf("%d:%s:%s", index, filepath.ToSlash(config), name), Scope: index, Source: filepath.ToSlash(config), Name: name, Kind: domain.NativeConfigurationUnsupported}
					if value, ok := configValues[name].(string); ok && domain.NativeConfigurationSettingSupported(name, value) {
						entry.Kind = domain.NativeConfigurationSetting
						entry.Value = value
					}
					// Unknown names might themselves contain secrets. Publish only a closed
					// classification; their original values and nested keys never leave Worker.
					if entry.Kind == domain.NativeConfigurationUnsupported {
						entry.Key = fmt.Sprintf("%d:%s:unsupported-%d", index, filepath.ToSlash(config), len(snapshot.Entries))
						entry.Name = unsupportedConfigurationClass(name)
					}
					snapshot.Entries = append(snapshot.Entries, entry)
				}
			}
			// Native Codex chooses the first nonempty instruction file. An empty
			// override falls back to the ordinary instructions in the same scope.
			instruction := 1
			if !snapshot.Files[len(snapshot.Files)-2].Usable {
				instruction = 2
			}
			if len(strings.TrimSpace(string(values[instruction]))) > 0 {
				snapshot.Entries = append(snapshot.Entries, domain.NativeConfigurationEntry{Key: fmt.Sprintf("%d:%s", index, paths[instruction]), Scope: index, Source: paths[instruction], Name: "instructions", Kind: domain.NativeConfigurationInstruction, Value: string(values[instruction])})
			}
		}()
		if err != nil {
			return domain.NativeConfigurationSnapshot{}, err
		}
	}
	snapshot.Digest = snapshot.SourceDigest()
	if snapshot.Validate() != nil {
		return domain.NativeConfigurationSnapshot{}, domain.NativeConfigurationInvalid()
	}
	if request.ExpectedDigest != "" && request.ExpectedDigest != snapshot.Digest {
		return domain.NativeConfigurationSnapshot{}, domain.Fail(domain.Conflict, "The selected native source changed before import.", "Obtain a fresh preview and confirm the selected entries again.")
	}
	return snapshot, nil
}
func unsupportedConfigurationClass(name string) string {
	switch name {
	case "mcp_servers":
		return "MCP settings (not imported)"
	case "hooks":
		return "hooks (not imported)"
	case "plugins":
		return "plugins (not imported)"
	case "model", "model_provider", "profiles", "profile":
		return "model or profile selection (unsupported)"
	case "api_key", "auth", "token", "credentials", "env", "model_providers":
		return "credential-bearing configuration (excluded)"
	}
	return "unsupported native configuration"
}
func readConfigurationFile(root *os.Root, name string) ([]byte, bool, error) {
	for current := name; current != "."; current = filepath.Dir(current) {
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, false, domain.NativeConfigurationInvalid()
		}
	}
	file, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, domain.NativeConfigurationInvalid()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return nil, false, domain.NativeConfigurationInvalid()
	}
	raw, err := io.ReadAll(io.LimitReader(file, 128<<10+1))
	if err != nil || len(raw) > 128<<10 || !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
		return nil, false, domain.NativeConfigurationInvalid()
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, false, domain.Fail(domain.Conflict, "The selected source changed during its read.", "Obtain a new explicit preview.")
	}
	return raw, true, nil
}
