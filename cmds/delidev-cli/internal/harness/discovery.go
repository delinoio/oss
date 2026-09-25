// Package harness owns installed native harness discovery and adapters on the
// Worker. Discovery never installs a harness or uses an account for inference.
package harness

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const probeOutputLimit = 64 << 10
const probeTimeout = 10 * time.Second

type DiscoveryConfig struct {
	Root    string
	OwnerID domain.ID
	Logger  *slog.Logger
}

func Discover(ctx context.Context, config DiscoveryConfig, input domain.HarnessDiscoveryInput) (result domain.HarnessDiscoveryOutput, returned error) {
	root, owner := config.Root, config.OwnerID
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	logger = logger.With("job_id", owner)

	if err := owner.Validate(); err != nil {
		return result, err
	}
	if input.Revision == 0 {
		return result, domain.Fail(domain.InvalidArgument, "Discovery revision is required.", "Use an accepted discovery job.")
	}
	if err := input.Selections.Validate(); err != nil {
		return result, err
	}
	processRoot := filepath.Join(root, "processes")
	probeRoot := filepath.Join(root, "probes")
	for _, directory := range []string{processRoot, filepath.Join(processRoot, string(owner)), probeRoot} {
		if err := security.PrivateDir(directory); err != nil {
			return result, err
		}
	}
	directory, err := os.MkdirTemp(probeRoot, string(owner)+"-")
	if err != nil {
		return result, err
	}
	if err := security.PrivateDir(directory); err != nil {
		return result, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return result, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return result, err
	}
	defer func() {
		// Even a failed probe may have spawned descendants. Preserve its private
		// runtime if native ownership cannot prove that cleanup is safe.
		if err := process.ReconcileOwner(processRoot, owner); err != nil {
			result, returned = domain.HarnessDiscoveryOutput{}, err
			return
		}
		if err := os.RemoveAll(directory); err != nil && returned == nil {
			result, returned = domain.HarnessDiscoveryOutput{}, domain.Fail(domain.RecoveryRequired, "The private discovery runtime could not be removed.", "Inspect the Worker's retained probe directory before retrying.")
		}
	}()
	result.Installations = input.Selections.Installations()
	for index := range result.Installations {
		if err := ctx.Err(); err != nil {
			return domain.HarnessDiscoveryOutput{}, domain.SafeError(err)
		}
		i := &result.Installations[index]
		logger.InfoContext(ctx, "harness discovery started", "harness", i.Harness, "explicit_path", i.ExplicitPath != "")
		resolved, state := resolve(i.Harness, i.ExplicitPath)
		i.ResolvedPath, i.State = resolved, state
		if state == domain.InstallationUnchecked {
			home := filepath.Join(directory, string(i.Harness))
			env, err := probeEnvironment(home)
			if err != nil {
				return domain.HarnessDiscoveryOutput{}, err
			}
			args := []string{"--version"}
			if i.Harness == domain.GrokBuild {
				args = []string{"--no-auto-update", "--version"}
			}
			bounded, cancel := context.WithTimeout(ctx, probeTimeout)
			stdout := probeOutput{cancel: cancel, capture: true}
			stderr := probeOutput{cancel: cancel}
			err = process.Run(bounded, process.Config{Directory: processRoot, OwnerID: owner, Executable: resolved, Args: args, Env: env, Cwd: home, Stdout: &stdout, Stderr: &stderr, Logger: logger.With("harness", i.Harness)})
			cancel()
			if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
				return domain.HarnessDiscoveryOutput{}, err
			}
			if ctx.Err() != nil {
				return domain.HarnessDiscoveryOutput{}, domain.SafeError(ctx.Err())
			}
			if err != nil || stdout.overflow || stderr.overflow {
				i.State = domain.InstallationFailed
				if err != nil && !stdout.overflow && !stderr.overflow {
					switch domain.SafeError(err).Code {
					case domain.NotFound:
						i.State = domain.InstallationMissing
					case domain.PermissionDenied:
						i.State = domain.InstallationDenied
					case domain.Unsupported:
						i.State = domain.InstallationIncompatible
					}
				}
			} else {
				i.Version = parseVersion(i.Harness, stdout.buffer.String())
				if i.Version == "" {
					i.State = domain.InstallationIncompatible
				} else {
					i.State = domain.InstallationDetected
				}
			}
		}
		i.Problem = domain.InstallationProblem(i.State)
		if input.VerifyProtocol && i.State == domain.InstallationDetected {
			i.Protocol = &domain.ProtocolObservation{Protocol: domain.ProtocolFor(i.Harness), State: domain.ProtocolUnsupported}
			if i.Harness == domain.Codex || i.Harness == domain.ClaudeCode {
				home := filepath.Join(directory, string(i.Harness))
				env, err := probeEnvironment(home)
				if err != nil {
					return domain.HarnessDiscoveryOutput{}, err
				}
				bounded, cancel := context.WithTimeout(ctx, probeTimeout)
				config := process.Config{Directory: processRoot, OwnerID: owner, Executable: i.ResolvedPath, Env: env, Cwd: home, Logger: logger.With("harness", i.Harness)}
				switch i.Harness {
				case domain.Codex:
					var client *codex.Client
					client, err = codex.Open(bounded, codex.Config{Process: config, Version: i.Version, Home: filepath.Join(home, "codex")})
					if err == nil {
						err = client.Close()
					}
				case domain.ClaudeCode:
					err = claude.Probe(bounded, claude.ProbeConfig{Process: config, Version: i.Version, Home: filepath.Join(home, "claude")})
				}
				cancel()
				if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
					return domain.HarnessDiscoveryOutput{}, err
				}
				if ctx.Err() != nil {
					return domain.HarnessDiscoveryOutput{}, domain.SafeError(ctx.Err())
				}
				if err == nil {
					i.Protocol.State = domain.ProtocolVerified
					i.ProtocolVerified = true
				} else if domain.SafeError(err).Code != domain.Unsupported {
					i.Protocol.State = domain.ProtocolFailed
				}
			}
			i.Protocol.Problem = domain.ProtocolProblem(i.Protocol.State)
			logger.InfoContext(ctx, "harness protocol validation completed", "harness", i.Harness, "state", i.Protocol.State)
		}
		logger.InfoContext(ctx, "harness discovery completed", "harness", i.Harness, "state", i.State)
	}
	return result, nil
}

// Continue draining after the limit while cancellation tears down the owned
// tree. Returning an output error would abandon the native completion protocol.
type probeOutput struct {
	buffer   bytes.Buffer
	count    int
	overflow bool
	capture  bool
	cancel   context.CancelFunc
}

func (o *probeOutput) Write(p []byte) (int, error) {
	remaining := max(0, probeOutputLimit-o.count)
	if o.capture {
		_, _ = o.buffer.Write(p[:min(len(p), remaining)])
	}
	if len(p) > remaining {
		o.overflow = true
		o.cancel()
	}
	o.count = min(probeOutputLimit, o.count+min(len(p), probeOutputLimit))
	return len(p), nil
}

func executableName(harness domain.Harness) string {
	switch harness {
	case domain.Codex:
		return "codex"
	case domain.ClaudeCode:
		return "claude"
	case domain.OpenCode:
		return "opencode"
	case domain.GrokBuild:
		return "grok"
	default:
		return ""
	}
}

func resolve(harness domain.Harness, explicit string) (string, domain.InstallationState) {
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", domain.InstallationIncompatible
		}
		return checkExecutable(explicit)
	}
	name := executableName(harness)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, directory := range filepath.SplitList(cleanPath()) {
		candidate := filepath.Join(directory, name)
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		// A discovered but broken/denied candidate is visible, never silently
		// replaced by another installation later in PATH.
		return checkExecutable(candidate)
	}
	return "", domain.InstallationMissing
}
func checkExecutable(path string) (string, domain.InstallationState) {
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", domain.InstallationMissing
	}
	if errors.Is(err, os.ErrPermission) {
		return "", domain.InstallationDenied
	}
	if err != nil {
		return "", domain.InstallationFailed
	}
	info, err := os.Stat(resolved)
	if errors.Is(err, os.ErrPermission) {
		return "", domain.InstallationDenied
	}
	if err != nil {
		return "", domain.InstallationFailed
	}
	if !info.Mode().IsRegular() {
		return "", domain.InstallationIncompatible
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return resolved, domain.InstallationDenied
	}
	// Windows shell wrappers are not native CreateProcess executables. Never
	// interpolate their paths into cmd.exe; require the installed native binary.
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(resolved), ".exe") {
		return resolved, domain.InstallationIncompatible
	}
	return resolved, domain.InstallationUnchecked
}

func cleanPath() string {
	var paths []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			paths = append(paths, entry)
		}
	}
	return strings.Join(paths, string(os.PathListSeparator))
}
func probeEnvironment(home string) ([]string, error) {
	return PrivateRuntimeEnvironment(home)
}

// PrivateRuntimeEnvironment prepares a DeliDev-owned native runtime. Only
// bounded system lookup context survives; callers supply scoped execution
// credentials separately and retain this directory when native history exists.
func PrivateRuntimeEnvironment(home string) ([]string, error) {
	for _, name := range []string{"", "config", "cache", "data", "state", "tmp", "codex", "claude", "opencode"} {
		if err := security.PrivateDir(filepath.Join(home, name)); err != nil {
			return nil, err
		}
	}
	// Only lookup/system context survives. In particular, no provider tokens,
	// SSH agent, proxy, NODE_OPTIONS, dynamic loader or user config is inherited.
	env := []string{"PATH=" + cleanPath(), "HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + filepath.Join(home, "config"), "LOCALAPPDATA=" + filepath.Join(home, "data"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_STATE_HOME=" + filepath.Join(home, "state"), "TMPDIR=" + filepath.Join(home, "tmp"), "TMP=" + filepath.Join(home, "tmp"), "TEMP=" + filepath.Join(home, "tmp"), "CODEX_HOME=" + filepath.Join(home, "codex"), "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "claude"), "OPENCODE_CONFIG_DIR=" + filepath.Join(home, "opencode"), "NO_COLOR=1", "CI=1", "DISABLE_AUTOUPDATER=1", "DISABLE_UPDATES=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true", "OPENCODE_DISABLE_MODELS_FETCH=true", "OPENCODE_DISABLE_LSP_DOWNLOAD=true", "OPENCODE_DISABLE_CLAUDE_CODE=true", `OPENCODE_CONFIG_CONTENT={"autoupdate":false}`}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "WINDIR"} {
			if value := os.Getenv(name); value != "" {
				env = append(env, name+"="+value)
			}
		}
	}
	return env, nil
}

func parseVersion(harness domain.Harness, raw string) string {
	value := strings.TrimSpace(raw)
	switch harness {
	case domain.Codex:
		value = strings.TrimPrefix(value, "codex-cli ")
	case domain.ClaudeCode:
		value = strings.TrimSuffix(value, " (Claude Code)")
	case domain.GrokBuild:
		value = strings.TrimPrefix(value, "grok ")
	}
	value = strings.TrimPrefix(value, "v")
	if !domain.ValidInstallationVersion(value) {
		return ""
	}
	return value
}
