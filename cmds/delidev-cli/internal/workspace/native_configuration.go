// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func nativeConfigUnavailable() error {
	return domain.Fail(domain.Unsupported, "The selected native configuration cannot be imported safely.", "Keep unsupported content in its original CLI configuration.")
}
func readNativeConfigurationFile(root *os.Root, name string) ([]byte, error) {
	components := strings.Split(name, "/")
	parent := root
	var owned []*os.Root
	defer func() {
		for i := len(owned) - 1; i >= 0; i-- {
			owned[i].Close()
		}
	}()
	for _, part := range components[:len(components)-1] {
		info, err := parent.Lstat(part)
		if err != nil {
			return nil, err
		}
		child, err := openVerifiedChildRoot(parent, part, info)
		if err != nil {
			return nil, err
		}
		owned = append(owned, child)
		parent = child
	}
	leaf := components[len(components)-1]
	before, err := parent.Lstat(leaf)
	if err != nil {
		return nil, err
	}
	file, err := openVerifiedEntry(parent, leaf, before)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, readFailure()
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, readChanged()
	}
	if len(raw) > 64<<10 || domain.Text(string(raw), "native import contents", 64<<10, true) != nil {
		return nil, nativeConfigUnavailable()
	}
	return raw, nil
}

// Only explicitly selected roots are opened. Never consult ~/.claude.json,
// credentials, managed policy or generated project memory.
func readClaudeConfigurationSources(ctx context.Context, userRoot, projectRoot string) (domain.NativeConfigurationSnapshot, error) {
	result := domain.NativeConfigurationSnapshot{Entries: []domain.NativeConfigurationEntry{}}
	digests := map[string]string{}
	totalRead := 0
	walked := 0
	checkWalk := func(name string) error {
		walked++
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walked > 1024 || strings.Count(name, "/") > 16 {
			return nativeConfigUnavailable()
		}
		return nil
	}
	type source struct {
		root       string
		scope      domain.NativeConfigurationSource
		precedence uint32
		settings   string
	}
	sources := []source{}
	if userRoot != "" {
		sources = append(sources, source{userRoot, domain.NativeConfigurationUser, 1, ".claude/settings.json"})
	}
	if projectRoot != "" {
		sources = append(sources, source{projectRoot, domain.NativeConfigurationProject, 2, ".claude/settings.json"}, source{projectRoot, domain.NativeConfigurationLocal, 3, ".claude/settings.local.json"})
	}
	add := func(e domain.NativeConfigurationEntry) error {
		e.ID = domain.NativeConfigurationEntryID(e)
		e.Activation = domain.NativeConfigurationDisabled
		if e.Supported {
			e.Digest = domain.NativeConfigurationEntryDigest(e)
		}
		if err := e.Validate(); err != nil {
			return err
		}
		result.Entries = append(result.Entries, e)
		if len(result.Entries) > domain.MaxNativeConfigurationEntries {
			return domain.Fail(domain.ResourceExhausted, "Too many native import entries.", "Select a smaller explicit source scope.")
		}
		return nil
	}
	for _, src := range sources {
		if ctx.Err() != nil {
			return domain.NativeConfigurationSnapshot{}, ctx.Err()
		}
		info, err := os.Lstat(src.root)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return domain.NativeConfigurationSnapshot{}, readUnsupported()
		}
		root, err := os.OpenRoot(src.root)
		if err != nil {
			return domain.NativeConfigurationSnapshot{}, readFailure()
		}
		opened, e := root.Stat(".")
		current, ce := os.Lstat(src.root)
		if e != nil || ce != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !os.SameFile(info, current) {
			root.Close()
			return domain.NativeConfigurationSnapshot{}, readChanged()
		}
		err = func() error {
			read := func(name string) ([]byte, error) {
				raw, err := readNativeConfigurationFile(root, name)
				if errors.Is(err, fs.ErrNotExist) {
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				totalRead += len(raw)
				if totalRead > domain.MaxNativeConfigurationBytes {
					return nil, domain.Fail(domain.ResourceExhausted, "Native source bytes exceed their read bound.", "Select a smaller explicit source scope.")
				}
				h := sha256.Sum256(raw)
				digests[string(src.scope)+":"+name] = hex.EncodeToString(h[:])
				return raw, nil
			}
			raw, err := read(src.settings)
			if err != nil {
				return err
			}
			if raw != nil {
				values := map[string]json.RawMessage{}
				if domain.Decode(raw, &values) != nil {
					return nativeConfigUnavailable()
				}
				keys := make([]string, 0, len(values))
				for k := range values {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				for _, key := range keys {
					e := domain.NativeConfigurationEntry{Source: src.scope, Path: src.settings, Name: key, Kind: domain.NativeConfigurationSetting, Precedence: src.precedence, Supported: domain.NativeConfigurationSettingAllowed(key)}
					if e.Supported {
						if !domain.NativeConfigurationSettingSupported(key, values[key]) {
							e.Supported = false
							e.Reason = "credential-or-unsupported-setting"
						} else {
							e.Value = values[key]
						}
					} else if key == "hooks" {
						e.Kind = domain.NativeConfigurationHook
						e.Supported = domain.NativeConfigurationJSONSafe(values[key])
						if e.Supported {
							e.Files = []domain.NativeConfigurationFile{{Path: "hooks.json", Contents: string(values[key])}}
						}
					} else {
						e.Reason = "unsupported-setting"
					}
					if !e.Supported && e.Reason == "" {
						e.Reason = "credential-or-unsupported-definition"
					}
					if err := add(e); err != nil {
						return err
					}
				}
			}
			if src.scope == domain.NativeConfigurationLocal {
				raw, e := read("CLAUDE.local.md")
				if e != nil {
					return e
				}
				if raw != nil {
					entry := domain.NativeConfigurationEntry{Source: src.scope, Path: "CLAUDE.local.md", Name: "CLAUDE.local.md", Kind: domain.NativeConfigurationInstruction, Precedence: src.precedence, Supported: domain.NativeConfigurationTextSafe(string(raw))}
					if entry.Supported {
						entry.Files = []domain.NativeConfigurationFile{{Path: "CLAUDE.local.md", Contents: string(raw)}}
					} else {
						entry.Reason = "credential-bearing-instructions"
					}
					return add(entry)
				}
				return nil
			}
			instructionPaths := []string{".claude/CLAUDE.md"}
			if src.scope == domain.NativeConfigurationProject {
				instructionPaths = append(instructionPaths, "CLAUDE.md")
			}
			if err := fs.WalkDir(root.FS(), ".claude/rules", func(name string, d fs.DirEntry, err error) error {
				if e := checkWalk(name); e != nil {
					return e
				}
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				if err != nil {
					return readFailure()
				}
				if d.Type()&os.ModeSymlink != 0 {
					return readUnsupported()
				}
				if !d.IsDir() {
					if path.Ext(name) != ".md" {
						return nativeConfigUnavailable()
					}
					instructionPaths = append(instructionPaths, name)
				}
				if len(instructionPaths) > domain.MaxNativeConfigurationEntries {
					return nativeConfigUnavailable()
				}
				return nil
			}); err != nil {
				return err
			}
			slices.Sort(instructionPaths)
			for _, name := range instructionPaths {
				raw, err := read(name)
				if err != nil {
					return err
				}
				if raw == nil {
					continue
				}
				e := domain.NativeConfigurationEntry{Source: src.scope, Path: name, Name: path.Base(name), Kind: domain.NativeConfigurationInstruction, Precedence: src.precedence, Supported: domain.NativeConfigurationTextSafe(string(raw))}
				if e.Supported {
					e.Files = []domain.NativeConfigurationFile{{Path: path.Base(name), Contents: string(raw)}}
				} else {
					e.Reason = "credential-bearing-instructions"
				}
				if err := add(e); err != nil {
					return err
				}
			}
			for _, family := range []struct {
				directory string
				kind      domain.NativeConfigurationEntryKind
			}{{".claude/skills", domain.NativeConfigurationSkill}, {".claude/plugins", domain.NativeConfigurationPlugin}} {
				directories := []string{}
				err := fs.WalkDir(root.FS(), family.directory, func(name string, d fs.DirEntry, err error) error {
					if e := checkWalk(name); e != nil {
						return e
					}
					if errors.Is(err, fs.ErrNotExist) {
						return nil
					}
					if err != nil {
						return readFailure()
					}
					if d.Type()&os.ModeSymlink != 0 {
						return readUnsupported()
					}
					if d.IsDir() && name != family.directory {
						directories = append(directories, name)
						return fs.SkipDir
					}
					return nil
				})
				if err != nil {
					return err
				}
				slices.Sort(directories)
				for _, directory := range directories {
					e := domain.NativeConfigurationEntry{Source: src.scope, Path: directory, Name: path.Base(directory), Kind: family.kind, Precedence: src.precedence, Supported: true}
					if err := fs.WalkDir(root.FS(), directory, func(name string, d fs.DirEntry, err error) error {
						if e := checkWalk(name); e != nil {
							return e
						}
						if err != nil {
							return readFailure()
						}
						if d.Type()&os.ModeSymlink != 0 {
							return readUnsupported()
						}
						if d.IsDir() {
							return nil
						}
						relative := strings.TrimPrefix(name, directory+"/")
						lower := strings.ToLower(relative)
						if strings.Contains(lower, "credential") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, ".env") {
							return nativeConfigUnavailable()
						}
						raw, err := read(name)
						if err != nil {
							return err
						}
						if !domain.NativeConfigurationTextSafe(string(raw)) || strings.HasSuffix(name, ".json") && !domain.NativeConfigurationJSONSafe(raw) {
							return nativeConfigUnavailable()
						}
						e.Files = append(e.Files, domain.NativeConfigurationFile{Path: relative, Contents: string(raw)})
						if len(e.Files) > 128 {
							return nativeConfigUnavailable()
						}
						return nil
					}); err != nil {
						if domain.SafeError(err).Code == domain.Unsupported {
							e.Supported = false
							e.Reason = "unsupported-or-credential-bearing-package"
							e.Files = nil
						} else {
							return err
						}
					}
					if e.Supported && len(e.Files) == 0 {
						continue
					}
					if err := add(e); err != nil {
						return err
					}
				}
			}
			if src.scope == domain.NativeConfigurationProject {
				raw, err := read(".mcp.json")
				if err != nil {
					return err
				}
				if raw != nil {
					var definitions struct {
						Servers map[string]json.RawMessage `json:"mcpServers"`
					}
					if domain.Decode(raw, &definitions) != nil {
						return nativeConfigUnavailable()
					}
					names := make([]string, 0, len(definitions.Servers))
					for name := range definitions.Servers {
						names = append(names, name)
					}
					slices.Sort(names)
					for _, name := range names {
						content := definitions.Servers[name]
						e := domain.NativeConfigurationEntry{Source: src.scope, Path: ".mcp.json", Name: name, Kind: domain.NativeConfigurationMCP, Precedence: src.precedence, Supported: domain.NativeConfigurationJSONSafe(content)}
						if e.Supported {
							e.Files = []domain.NativeConfigurationFile{{Path: "mcp.json", Contents: string(content)}}
						} else {
							e.Reason = "credential-bearing-definition"
						}
						if err := add(e); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}()
		root.Close()
		if err != nil {
			return domain.NativeConfigurationSnapshot{}, err
		}
	}
	result.Digest = domain.NativeConfigurationDigest(digests)
	slices.SortStableFunc(result.Entries, func(a, b domain.NativeConfigurationEntry) int {
		if a.Precedence < b.Precedence {
			return -1
		}
		if a.Precedence > b.Precedence {
			return 1
		}
		return strings.Compare(a.Path+":"+a.Name, b.Path+":"+b.Name)
	})
	return result, result.Validate()
}

func (m *Manager) ReadClaudeConfiguration(ctx context.Context, request ReadRequest) (domain.NativeConfigurationSnapshot, error) {
	scope := request.ClaudeConfiguration
	if scope == nil || request.Query.Operation != "" || request.Skills != nil || request.PRCandidate != nil || scope.MachineID != request.Preparation.MachineID || scope.WorkerDeviceID.Validate() != nil || scope.WorkerInstanceID.Validate() != nil {
		return domain.NativeConfigurationSnapshot{}, ResultUncertain()
	}
	if scope.ProjectID == "" && scope.ProjectRoot != "" || scope.ProjectID != "" && scope.ProjectRoot == "" {
		return domain.NativeConfigurationSnapshot{}, ResultUncertain()
	}
	user := ""
	if scope.IncludeUser {
		var err error
		user, err = os.UserHomeDir()
		if err != nil {
			return domain.NativeConfigurationSnapshot{}, readFailure()
		}
	}
	return readClaudeConfigurationSources(ctx, user, scope.ProjectRoot)
}
