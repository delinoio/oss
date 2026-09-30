// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type prGitScope struct {
	Version           uint32                `json:"version"`
	Root              string                `json:"root"`
	RepositoryPath    string                `json:"repository_path"`
	Remote            string                `json:"remote"`
	Claim             executionClaim        `json:"claim"`
	Selection         domain.PRFixExecution `json:"selection"`
	Transport         prGitTransport        `json:"transport"`
	GitExecutable     string                `json:"git_executable"`
	GitDigest         string                `json:"git_digest"`
	ToolDigest        string                `json:"tool_digest"`
	EnvironmentDigest string                `json:"environment_digest"`
}

type PRGitTool struct {
	manager             *Manager
	scope               prGitScope
	path                string
	raw                 []byte
	environment         []string
	bridge              *prGitBridge
	configurationDigest string
	localName           string
	localEmail          string
	proofKey            [32]byte
}

func toolFailure() error {
	return domain.Fail(domain.RecoveryRequired, "The original PR Git tool ownership is unconfirmed.", "Preserve its attempt and native process evidence; never replay the push.")
}
func hashTool(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

const prGitEnvironmentPrefix = "DELIDEV_PR_GIT_"

type prGitPreparationPhase string

const (
	prGitPreflightPhase   prGitPreparationPhase = "workspace-preflight"
	prGitWriteAccessPhase prGitPreparationPhase = "write-access"
	prGitPublicationPhase prGitPreparationPhase = "tool-publication"
)

func originalPRGitEnvironment() []string {
	values := map[string]string{}
	for _, entry := range gitEnvironment() {
		name, value, _ := strings.Cut(entry, "=")
		values[strings.ToUpper(name)] = value
	}
	out := make([]string, 0, len(values))
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	slices.Sort(out)
	return out
}
func environmentDigest(env []string) string { raw, _ := json.Marshal(env); return hashTool(raw) }
func toolName() string {
	if runtime.GOOS == "windows" {
		return "git.exe"
	}
	return "git"
}

// The native launcher is the existing executable with a fixed private entry
// point. No shell or second argument parse is involved, including on Windows.
// The launcher is only an authenticated local client. Original Git lookup
// and authentication context remains in the independently owned Worker bridge.
func (l *ExecutionLease) PreparePRGitTool(ctx context.Context, selection domain.PRFixExecution, input PrepareRequest, manifest Manifest) (_ *PRGitTool, returnedErr error) {
	phase := prGitPreflightPhase
	defer func() {
		if returnedErr != nil {
			l.manager.Logger.WarnContext(ctx, "manual_pr_git_preparation_failed", "attempt_id", selection.AttemptID, "execution_id", l.claim.ExecutionID, "phase", phase, "code", domain.SafeError(returnedErr).Code)
		}
	}()
	if selection.Validate() != nil || input.SessionID != l.claim.SessionID || manifest.SessionID != l.claim.SessionID {
		return nil, toolFailure()
	}
	current, err := l.manager.readExecutionClaim(l.claim.SessionID)
	if err != nil || current != l.claim || current.State != executionClaimActive {
		return nil, toolFailure()
	}
	var spec RepositorySpec
	var path string
	for i, r := range input.Repositories {
		if r.ID == selection.Target.Target.RepositoryID {
			spec = r
			path = manifest.Repositories[i].Path
		}
	}
	if path == "" {
		return nil, toolFailure()
	}
	spec.PRTarget = &selection.Target
	git := l.manager.Git
	git.OwnerID = l.claim.JobID
	git.readOnly = true
	if err := git.preflightPRWorkspace(ctx, path, spec, prHeadOrDetached); err != nil {
		return nil, err
	}
	inspection, err := git.Inspect(ctx, path)
	if err != nil {
		return nil, err
	}
	transport, err := git.prTransport(ctx, inspection, spec.PreferredRemote, selection.Target)
	if err != nil {
		return nil, err
	}
	phase = prGitWriteAccessPhase
	address := transport.address(selection.Target.HeadRepository.Owner, selection.Target.HeadRepository.Name)
	if err := git.rejectPRPushRewrite(ctx, path, address); err != nil {
		return nil, err
	}
	// Dry-run checks receive-pack access with the selected Worker's native Git
	// identity. It is independently owned read/preflight, never publication.
	if _, err := git.run(ctx, path, "-c", "http.followRedirects=false", "push", "--dry-run", "--porcelain", "--", address, selection.Target.HeadSHA+":refs/heads/"+selection.Target.HeadRef); err != nil {
		if domain.SafeError(err).Cause != "git_exit" {
			return nil, err
		}
		return nil, domain.Fail(domain.MissingInput, "The execution Worker lacks PR Git push access.", "Prepare native Git write authentication on this Worker; the server lookup PAT cannot be substituted.")
	}
	configuration, err := git.run(ctx, path, "config", "--null", "--show-origin", "--list")
	if err != nil {
		return nil, err
	}
	name, err := git.run(ctx, path, "config", "--get", "user.name")
	if err != nil {
		return nil, domain.Fail(domain.MissingInput, "The Worker Git commit identity is missing.", "Configure an explicit native Git user name and email on this Worker.")
	}
	email, err := git.run(ctx, path, "config", "--get", "user.email")
	if err != nil || domain.Text(trimGit(name), "Git user name", 1024, true) != nil || domain.Text(trimGit(email), "Git user email", 1024, true) != nil || strings.ContainsAny(trimGit(name)+trimGit(email), "\r\n") {
		return nil, domain.Fail(domain.MissingInput, "The Worker Git commit identity is invalid.", "Configure a bounded native Git user name and email on this Worker.")
	}
	phase = prGitPublicationPhase
	dir := filepath.Join(l.manager.Root, "pr-git", string(l.claim.ExecutionID))
	if err := security.PrivateDir(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, toolFailure()
	}
	if err := security.SyncParent(dir); err != nil {
		return nil, toolFailure()
	}
	scopePath := filepath.Join(dir, "scope.json")
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	toolPath := filepath.Join(dir, toolName())
	executableBytes, err := readPRExecutable(executable)
	if err != nil || len(executableBytes) > 128<<20 {
		return nil, toolFailure()
	}
	if err := security.WriteAtomic(toolPath, executableBytes); err != nil {
		return nil, err
	}
	if err := os.Chmod(toolPath, 0700); err != nil {
		return nil, err
	}
	nativeGit := git.Executable
	if nativeGit == "" {
		nativeGit, err = exec.LookPath("git")
	}
	if err != nil {
		return nil, toolFailure()
	}
	nativeGit, err = filepath.EvalSymlinks(nativeGit)
	if err != nil || !filepath.IsAbs(nativeGit) {
		return nil, toolFailure()
	}
	gitBytes, err := readPRExecutable(nativeGit)
	if err != nil || len(gitBytes) > 128<<20 {
		return nil, toolFailure()
	}
	environment := originalPRGitEnvironment()
	scope := prGitScope{Version: 1, Root: l.manager.Root, RepositoryPath: path, Remote: spec.PreferredRemote, Claim: l.claim, Selection: selection, Transport: transport, GitExecutable: nativeGit, GitDigest: hashTool(gitBytes), ToolDigest: hashTool(executableBytes), EnvironmentDigest: environmentDigest(environment)}
	raw, _ := json.Marshal(scope)
	if err := security.WriteAtomic(scopePath, raw); err != nil {
		return nil, err
	}
	tool := &PRGitTool{manager: l.manager, scope: scope, path: scopePath, raw: raw, environment: environment, configurationDigest: hashTool(configuration), localName: trimGit(name), localEmail: trimGit(email)}
	if _, err := rand.Read(tool.proofKey[:]); err != nil {
		return nil, toolFailure()
	}
	tool.bridge, err = startPRGitBridge(ctx, tool)
	if err != nil {
		return nil, err
	}
	l.manager.Logger.InfoContext(ctx, "manual_pr_git_ready", "attempt_id", selection.AttemptID, "execution_id", l.claim.ExecutionID)
	return tool, nil
}
func (t *PRGitTool) Environment(env []string) []string {
	out := slices.Clone(env)
	out = append(out, prGitEnvironmentPrefix+"SCOPE="+t.path)
	out = append(out, prGitEnvironmentPrefix+"ENDPOINT="+t.bridge.endpoint, prGitEnvironmentPrefix+"TOKEN="+t.bridge.token)
	for i, entry := range out {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") {
			out[i] = "PATH=" + filepath.Dir(t.path) + string(os.PathListSeparator) + value
		}
	}
	return out
}
func loadPRGitScope(path string) (prGitScope, []byte, error) {
	var scope prGitScope
	raw, err := security.ReadPrivate(path, 32<<10)
	if err != nil || domain.Decode(raw, &scope) != nil || scope.Version != 1 || scope.Selection.Validate() != nil || scope.Claim.State != executionClaimActive || !filepath.IsAbs(scope.Root) || path != filepath.Join(scope.Root, "pr-git", string(scope.Claim.ExecutionID), "scope.json") || !filepath.IsAbs(scope.RepositoryPath) || !canonicalCommit(scope.ToolDigest) {
		return scope, nil, toolFailure()
	}
	m := &Manager{Root: scope.Root}
	claim, err := m.readExecutionClaim(scope.Claim.SessionID)
	if err != nil || claim != scope.Claim {
		return scope, nil, toolFailure()
	}
	launcher, err := security.ReadPrivate(filepath.Join(filepath.Dir(path), toolName()), 128<<20)
	if err != nil || hashTool(launcher) != scope.ToolDigest {
		return scope, nil, toolFailure()
	}
	native, err := readPRExecutable(scope.GitExecutable)
	if err != nil || len(native) > 128<<20 || !filepath.IsAbs(scope.GitExecutable) || hashTool(native) != scope.GitDigest {
		return scope, nil, toolFailure()
	}
	return scope, raw, nil
}
func (s prGitScope) git(environment []string) Git {
	return Git{Executable: s.GitExecutable, ProcessRoot: filepath.Join(s.Root, "processes"), OwnerID: s.Claim.JobID, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), readOnly: true, Timeout: 2 * time.Minute, HooksDir: filepath.Join(s.Root, "empty-hooks"), environment: environment}
}
func (s prGitScope) addresses() (string, string) {
	t := s.Selection.Target
	return s.Transport.address(t.Target.Owner, t.Target.Name), s.Transport.address(t.HeadRepository.Owner, t.HeadRepository.Name)
}
func (s prGitScope) validateArgs(args []string) error {
	if len(args) == 0 || len(args) > 256 {
		return toolFailure()
	}
	for _, a := range args {
		if len(a) > 4096 || strings.ContainsRune(a, 0) {
			return toolFailure()
		}
	}
	switch args[0] {
	case "delidev-target":
		if len(args) != 1 {
			return toolFailure()
		}
	case "status", "diff", "log", "show", "rev-parse", "add", "commit":
	case "merge":
		if !s.Selection.Conflict || s.Selection.Strategy != domain.MergeConflictStrategy || !slices.Equal(args, []string{"merge", "--no-edit", s.Selection.Target.BaseSHA}) {
			return toolFailure()
		}
	case "rebase":
		if !s.Selection.Conflict || s.Selection.Strategy != domain.RebaseConflictStrategy || !(slices.Equal(args, []string{"rebase", s.Selection.Target.BaseSHA}) || slices.Equal(args, []string{"rebase", "--continue"}) || slices.Equal(args, []string{"rebase", "--abort"})) {
			return toolFailure()
		}
	case "fetch":
		base, _ := s.addresses()
		expected := []string{"fetch", "--no-tags", "--no-prune", "--no-prune-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--no-auto-maintenance", "--no-write-commit-graph", "--refmap=", "--", base, "refs/heads/" + s.Selection.Target.BaseRef}
		if !slices.Equal(args, expected) {
			return toolFailure()
		}
	case "push":
		_, head := s.addresses()
		expected := []string{"push", "--porcelain"}
		if s.Selection.Conflict && s.Selection.Strategy == domain.RebaseConflictStrategy {
			expected = append(expected, "--force-with-lease=refs/heads/"+s.Selection.Target.HeadRef+":"+s.Selection.Target.HeadSHA)
		}
		expected = append(expected, "--", head, "HEAD:refs/heads/"+s.Selection.Target.HeadRef)
		if !slices.Equal(args, expected) {
			return domain.Fail(domain.PermissionDenied, "This PR push does not match its original source, branch and lease.", "Use the exact selected source/refspec and expected-head lease; unrestricted force is unavailable.")
		}
	default:
		return domain.Fail(domain.PermissionDenied, "This Git operation is outside the manual PR fix profile.", "Use supported reads, edits, commit, exact conflict resolution and one bound push.")
	}
	return nil
}

// RunPRGit is called only by the private native launcher. The harness supplies
// literal argv and decides when to commit/push; no server or Go controller
// initiates publication. The original native process owner contains this child.
func (t *PRGitTool) runOwned(ctx context.Context, args []string, stdout io.Writer) error {
	path := t.path
	_, rawScope, err := loadPRGitScope(path)
	if err != nil || !bytes.Equal(rawScope, t.raw) {
		return toolFailure()
	}
	scope := t.scope
	if err := scope.validateArgs(args); err != nil {
		return err
	}
	environment := t.environment
	if environmentDigest(environment) != scope.EnvironmentDigest {
		return toolFailure()
	}
	if args[0] == "delidev-target" {
		raw, err := json.Marshal(scope.commandPlan())
		if err != nil {
			return toolFailure()
		}
		_, err = stdout.Write(append(raw, '\n'))
		return err
	}
	git := scope.git(environment)
	if err := t.requireConfiguration(ctx, git); err != nil {
		return err
	}
	target := scope.Selection.Target
	base, head := scope.addresses()
	if args[0] == "push" {
		if err := git.rejectPRPushRewrite(ctx, scope.RepositoryPath, head); err != nil {
			return err
		}
		branch, branchErr := git.run(ctx, scope.RepositoryPath, "branch", "--show-current")
		if branchErr != nil || trimGit(branch) != "" && trimGit(branch) != target.HeadRef {
			return prWorkspaceChanged()
		}
		if err := git.requirePRBranch(ctx, scope.RepositoryPath, head, target.HeadRef, target.HeadSHA); err != nil {
			return err
		}
		if err := git.requirePRBranch(ctx, scope.RepositoryPath, base, target.BaseRef, target.BaseSHA); err != nil {
			return err
		}
		raw, err := git.run(ctx, scope.RepositoryPath, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return err
		}
		result := trimGit(raw)
		if result == target.HeadSHA || !canonicalCommit(result) {
			return domain.Fail(domain.Conflict, "No new PR commit exists to push.", "Commit the validated changes before the one authorized push.")
		}
		if !(scope.Selection.Conflict && scope.Selection.Strategy == domain.RebaseConflictStrategy) {
			if _, err := git.run(ctx, scope.RepositoryPath, "merge-base", "--is-ancestor", target.HeadSHA, "HEAD"); err != nil {
				return prWorkspaceChanged()
			}
		}
		// O_EXCL plus fsync precedes the native push. Lost success, failed push or
		// Worker reconnect can never grant a second invocation for this attempt.
		f, err := os.OpenFile(filepath.Join(filepath.Dir(path), "push.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return toolFailure()
		}
		claim := prGitPushClaim{SelectionDigest: scope.Selection.Digest(), Head: result}
		claim.MAC = t.pushMAC(claim)
		raw, _ = json.Marshal(claim)
		_, writeErr := f.Write(raw)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || security.SyncParent(f.Name()) != nil {
			return toolFailure()
		}
	}
	command := []string{"-C", scope.RepositoryPath, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + git.HooksDir, "-c", "http.followRedirects=false"}
	if args[0] != "push" && args[0] != "fetch" {
		// Local edits/filters/signing never receive native authentication lookup
		// context. Only the closed network operations use the retained identity.
		environment = t.localEnvironment()
		command = append(command, "-c", "user.name="+t.localName, "-c", "user.email="+t.localEmail, "-c", "commit.gpgSign=false")
	}
	command = append(command, args...)
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output := limitedOutput{limit: MaxGitOutput}
	err = process.Run(bounded, process.Config{Directory: git.ProcessRoot, OwnerID: git.OwnerID, Executable: scope.GitExecutable, Args: command, Env: environment, Cwd: scope.RepositoryPath, Stdout: &output, Stderr: io.Discard, Logger: git.Logger})
	if err != nil {
		return domain.Fail(domain.Unavailable, "The native PR Git command did not complete successfully.", "Inspect the retained attempt and Worker Git access without replaying its push.")
	}
	if _, err := stdout.Write(output.Bytes()); err != nil {
		return err
	}
	return nil
}

// The plan exposes non-secret exact operands through the native tool instead
// of trusting remote prose or asking the harness to guess a fork transport.
func (s prGitScope) commandPlan() map[string]any {
	base, head := s.addresses()
	target := s.Selection.Target
	push := []string{"push", "--porcelain"}
	if s.Selection.Conflict && s.Selection.Strategy == domain.RebaseConflictStrategy {
		push = append(push, "--force-with-lease=refs/heads/"+target.HeadRef+":"+target.HeadSHA)
	}
	push = append(push, "--", head, "HEAD:refs/heads/"+target.HeadRef)
	fetch := []string{"fetch", "--no-tags", "--no-prune", "--no-prune-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--no-auto-maintenance", "--no-write-commit-graph", "--refmap=", "--", base, "refs/heads/" + target.BaseRef}
	return map[string]any{"repository_path": s.RepositoryPath, "base_commit": target.BaseSHA, "expected_head": target.HeadSHA, "strategy": s.Selection.Strategy, "conflict": s.Selection.Conflict, "fetch_argv": fetch, "push_argv": push}
}

func (t *PRGitTool) VerifyPush(ctx context.Context) (p domain.PRPushProof) {
	defer func() {
		t.manager.Logger.InfoContext(ctx, "manual_pr_git_verified", "attempt_id", t.scope.Selection.AttemptID, "execution_id", t.scope.Claim.ExecutionID, "state", p.State)
	}()
	p = domain.PRPushProof{Version: 1, AttemptID: t.scope.Selection.AttemptID, ExecutionID: t.scope.Claim.ExecutionID, SelectionDigest: t.scope.Selection.Digest(), State: domain.PRPushUncertain, PreviousHead: t.scope.Selection.Target.HeadSHA, ObservedAt: time.Now().UTC()}
	scope, raw, err := loadPRGitScope(t.path)
	if err != nil || string(raw) != string(t.raw) {
		return p
	}
	git := scope.git(t.environment)
	if t.requireConfiguration(ctx, git) != nil {
		return p
	}
	_, head := scope.addresses()
	target := scope.Selection.Target
	raw, err = git.run(ctx, scope.RepositoryPath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return p
	}
	result := trimGit(raw)
	claimRaw, err := security.ReadPrivate(filepath.Join(filepath.Dir(t.path), "push.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		if git.requirePRBranch(ctx, scope.RepositoryPath, head, target.HeadRef, target.HeadSHA) == nil {
			p.State, p.ResultHead = domain.PRPushUnchanged, target.HeadSHA
		}
		return p
	}
	var claim prGitPushClaim
	if err != nil || domain.Decode(claimRaw, &claim) != nil || claim.SelectionDigest != p.SelectionDigest || !hmac.Equal([]byte(claim.MAC), []byte(t.pushMAC(claim))) || claim.Head != result || result == target.HeadSHA || !canonicalCommit(result) {
		return p
	}
	if git.requirePRBranch(ctx, scope.RepositoryPath, head, target.HeadRef, result) != nil {
		return p
	}
	raw, err = git.run(ctx, scope.RepositoryPath, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none")
	if err != nil || len(raw) != 0 {
		return p
	}
	inspection, err := git.Inspect(ctx, scope.RepositoryPath)
	if err != nil || inspection.Root != scope.RepositoryPath {
		return p
	}
	transport, err := git.prTransport(ctx, inspection, scope.Remote, target)
	if err != nil || transport != scope.Transport {
		return p
	}
	branch, err := git.run(ctx, scope.RepositoryPath, "branch", "--show-current")
	if err != nil || trimGit(branch) != "" && trimGit(branch) != target.HeadRef {
		return p
	}
	if scope.Selection.Conflict {
		if _, err := git.run(ctx, scope.RepositoryPath, "merge-base", "--is-ancestor", target.BaseSHA, result); err != nil {
			return p
		}
	} else {
		if _, err := git.run(ctx, scope.RepositoryPath, "merge-base", "--is-ancestor", target.HeadSHA, result); err != nil {
			return p
		}
	}
	raw, err = git.run(ctx, scope.RepositoryPath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || trimGit(raw) != result || git.requirePRBranch(ctx, scope.RepositoryPath, head, target.HeadRef, result) != nil {
		return p
	}
	p.State, p.ResultHead, p.ObservedAt = domain.PRPushVerified, result, time.Now().UTC()
	return p
}

// Git's read-only URL expansion does not inspect pushInsteadOf. A matching
// push-only rewrite can redirect an explicit URL despite correct ls-remote
// evidence. Reject it before dry-run/publication; remove this guard only when
// the profile independently validates and binds Git's effective push address.
func (g Git) rejectPRPushRewrite(ctx context.Context, root, address string) error {
	raw, exit, err := g.runCommand(ctx, root, "config", "--null", "--get-regexp", `^url\..*\.pushinsteadof$`)
	if err != nil {
		if exit == 1 {
			return nil
		}
		return err
	}
	for _, record := range bytes.Split(raw, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		_, value, ok := bytes.Cut(record, []byte{'\n'})
		if !ok {
			return toolFailure()
		}
		if strings.HasPrefix(address, string(value)) {
			return domain.Fail(domain.MissingInput, "The Worker's Git push URL is rewritten outside the selected transport.", "Configure the selected GitHub source transport explicitly; a push-only rewrite cannot substitute its destination.")
		}
	}
	return nil
}

func readPRExecutable(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return nil, toolFailure()
	}
	raw, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	if err != nil || len(raw) > 128<<20 || int64(len(raw)) != info.Size() {
		return nil, toolFailure()
	}
	return raw, nil
}

func (t *PRGitTool) requireConfiguration(ctx context.Context, git Git) error {
	raw, err := git.run(ctx, t.scope.RepositoryPath, "config", "--null", "--show-origin", "--list")
	if err != nil {
		return err
	}
	if hashTool(raw) != t.configurationDigest {
		t.manager.Logger.WarnContext(ctx, "manual_pr_git_scope_rejected", "attempt_id", t.scope.Selection.AttemptID, "execution_id", t.scope.Claim.ExecutionID, "phase", "git-configuration", "code", domain.Conflict)
		return prWorkspaceChanged()
	}
	return nil
}

type prGitPushClaim struct {
	SelectionDigest string `json:"selection_digest"`
	Head            string `json:"head"`
	MAC             string `json:"mac"`
}

func (t *PRGitTool) pushMAC(claim prGitPushClaim) string {
	claim.MAC = ""
	raw, _ := json.Marshal(claim)
	mac := hmac.New(sha256.New, t.proofKey[:])
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func (t *PRGitTool) localEnvironment() []string {
	out := []string{}
	for _, entry := range t.environment {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL":
			out = append(out, entry)
		}
	}
	home := filepath.Join(filepath.Dir(t.path), "local-home")
	return append(out, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0")
}
func (t *PRGitTool) Close() error { return t.bridge.close() }
