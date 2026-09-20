## Summary

Create `async-commit-hook`, an agent-aware local check runner exposed through the `ach` executable.

After a developer or coding agent commits, a post-commit hook durably submits the configured checks, returns a run receipt, and explains how to inspect or wait for the result through CLI or MCP. Checks execute against isolated committed source while development continues. A supplied agent skill teaches agents to verify the final commit before reporting validation complete.

Provide an optional single-computer daemon and an on-demand mode using the same persistent records. A statically hosted web application at https://ach.delino.io provides repository/worktree and branch views resembling local pull requests.

The MVP includes final validation gates, an acknowledgement inbox, structured failure reports, execution planning and diagnostics, result comparison, failed-check reruns, optional pre-push enforcement, and explicit self-update.

The first release is a full supported release, not a preview. There is no fixed deadline or workload latency/throughput SLA. Release requires completion of the agreed functionality and actual integration validation across the six supported platform/architecture targets.

## Evidence

- Source: the project owner's requirements and explicit decisions collected during PRD planning.
- Primary users: developers and coding agents working in local Git repositories and linked worktrees.
- Current gap: committing can start background validation without giving an agent a reliable receipt, completion gate, or recoverable record of results it still needs to inspect.
- Human users also need persistent, browser-accessible branch and worktree inspection after the hook and check processes have finished.
- The execution model was explicitly changed from GitHub Actions/act to user-defined commands in project configuration.
- Repository contracts establish Go as the default language, UUID v7 for new persisted entities, Rspack-family frontend tooling, Cloudflare Pages for static hosting, structured logging, and documentation-first project onboarding.
- Relevant contracts: `AGENTS.md`, `cmds/AGENTS.md`, `apps/AGENTS.md`, `docs/repository-defaults.md`, and the project/domain documentation templates.
- Searches for `async-commit-hook`, `local CI`, and `commit hook` found no duplicate issue.
- Issue #893, Runmoor, manages ephemeral GitHub Actions runners and is a separate product with no required integration.
- The repository currently has no `PRD` label.

## Current Gap

The repository does not provide a command-based local check runner combining:

- Non-blocking post-commit execution with durable, agent-readable receipts.
- CLI, MCP, and a supplied skill for tracking and verifying checks.
- Persistent local history shared by daemon and on-demand modes.
- Repository/worktree and branch views with changes, commits, checks, and logs.
- A pre-push gate bound to the actual commit being pushed.
- Explicit result acknowledgement and reliable recovery after agent context loss.

A successful commit must not be presented as successful validation. An older successful commit, incompatible execution, or superseded result must not satisfy the current commit's gate.

## Proposed Scope

### Runtime, ownership, and supported environments

- Use project ID `async-commit-hook` and executable name `ach`; do not ship an `async-commit-hook` executable alias.
- Implement the Go core and CLI under `cmds/async-commit-hook`.
- Implement the React/TypeScript static web application with Rsbuild under `apps/async-commit-hook`.
- Place versioned Connect schemas and generated client integration in the repository's protocol and package domains.
- Support macOS 13+, Windows 10 22H2+, and Ubuntu 22.04 LTS+, each on x64 and arm64.
- Run user commands on the host. Default to `sh` on macOS/Linux and PowerShell on Windows, with explicit per-check shell and OS selection.
- Docker is not a required execution backend. Users may invoke their own tools through configured commands.
- Support ordinary Git repositories and linked worktrees. Detect repositories requiring submodules or Git LFS and report them as unsupported rather than silently checking incomplete source.
- Support one developer OS account per computer. That account owns one daemon for all registered repositories and worktrees.
- Use English for the UI, CLI messages, skills, and documentation.

### Configuration and command graph

- Store committed project configuration in `.config/async-commit-hook.toml`.
- Store personal configuration in `~/.config/async-commit-hook/config.toml`.
- Start configuration, CLI JSON, and persisted-state contracts at version 1. Use typed enums and reject invalid or unsupported configurations with actionable diagnostics.
- Project configuration defines named commands, dependencies, applicable operating systems, shell selection, optional checks, declared environment inputs, report inputs, queue behavior, and an optional diff base.
- Personal configuration owns machine settings such as daemon mode, API port, state directory, local credential references, and optional retention policies.
- Execute the project configuration associated with the selected commit. Freeze the accepted execution configuration for each run.
- A later configuration change affects subsequent runs, not already accepted runs.
- Changes to mode, API port, or state directory require checks and server processes to be stopped before applying them.
- Validate missing dependencies, cycles, invalid report declarations, and contradictory execution settings before launching commands.
- Applicable checks are required by default. Explicitly optional checks may fail without failing the required-check gate.
- An execution with no applicable checks must not be reported as successful validation.
- When a check fails, dependent checks become `blocked`; independent checks continue.

### Registration and Git hooks

- Explicit repository registration establishes trust in that repository's configured commands and subsequent committed configuration changes.
- Installing post-commit integration must preserve existing hooks, hook managers, and `core.hooksPath` behavior.
- Installation, reinstallation, and removal must be idempotent and operate only on product-owned entries.
- If an existing hook setup cannot be integrated safely, provide a concrete manual integration command rather than overwriting it.
- Register and identify linked worktrees without conflating separate clones merely because they share a remote URL.
- The post-commit hook durably records the request before printing a queued receipt.
- The receipt includes the run ID, commit SHA, current state, CLI status/wait commands, corresponding MCP guidance, and `ach agent-guide`.
- Include a directly usable web URL in daemon mode and an `ach ui --run <run-id>` instruction in on-demand mode.
- Detach check execution from the committing terminal and its inherited I/O so Git can return while checks continue.
- A submission or startup failure must produce truthful diagnostics. Never print a queued receipt for a request that was not saved.
- Hook retries must not accidentally create duplicate automatic runs for the same submission. Explicit runs and reruns remain separate recorded attempts.
- Prevent managed check workspaces from recursively triggering the product's own automatic checks.

### Execution, queueing, and cancellation

- Prepare an isolated workspace containing the exact committed source. Uncommitted files and later edits in the developer's worktree must not enter the run.
- Dependency preparation can be expressed as ordinary named commands in the graph.
- Provide per-check `parallel`, `queue`, and `replace` policies, with `parallel` as the default.
- The default concurrency group is repository plus check name. Explicit group names can coordinate checks across worktrees and repositories.
- `queue` serializes eligible checks within the group. `replace` cancels the previous check before starting its replacement.
- Group coordination must work across both daemon-owned workers and independent on-demand workers.
- Do not introduce a product-wide concurrent-command cap or command execution timeout.
- Cancellation and replacement must target only processes belonging to the relevant check and account for descendant processes.
- Do not declare cancellation complete or start an exclusive replacement while the previous execution is still active.
- Preserve cancelled, replaced, interrupted, and blocked outcomes in history.
- Collect logs and configured reports before deleting the isolated workspace. Clean up workspaces after both successful and failed runs.
- Failures to collect evidence or clean up owned resources must remain visible through diagnostics.
- Do not automatically retry interrupted commands. Reconcile their processes, record interruption, and require an explicit rerun.
- Recover requests that had not started on the next applicable startup.

### Daemon and on-demand modes

- Default to `mode = "daemon"`; also support `mode = "on-demand"`.
- In daemon mode, hooks and commands start the daemon when needed. Concurrent starts must converge on one owner.
- Keep the daemon available after checks finish until explicitly stopped.
- Normal daemon stop drains active work. Explicit forced stop cancels owned checks.
- Do not install OS-login autostart services.
- In on-demand mode, use background workers whose lifetimes are tied to pending or active work. Leave no idle daemon after work completes.
- CLI and stdio MCP inspection must work without starting a persistent daemon.
- `ach ui` in on-demand mode starts a temporary local API server, prints a connection URL, and remains active until stopped.
- Stopping the temporary viewer must not cancel unrelated checks or delete their results.
- Both modes use the same records, IDs, queue coordination, acknowledgements, and result semantics.
- Changing modes must preserve history.
- Use local API port 46309 by default, configurable in personal settings. Report conflicts instead of automatically remapping.
- Use fixed frontend development port 46308 and update the repository development-port contract.

### Persistent data and retention

- Default to `~/.local/share/async-commit-hook` on all supported operating systems, overridable through personal `state_dir`.
- Store registry, run/check metadata, acknowledgement state, queue coordination, and comparison metadata in SQLite.
- Store logs and test reports as local files.
- Use UUID v7 for new persistent product identifiers while retaining Git object IDs for commits.
- Preserve the originating repository/worktree, observed branch, exact commit, accepted configuration, execution context, and attempt relationships.
- Treat branch names and paths as navigation context, not substitutes for commit identity.
- Retain results indefinitely by default.
- Allow optional age- and size-based cleanup and provide `ach prune` with a preview/dry-run.
- Configured retention also applies to unacknowledged completed records. Protect pending and active execution data.
- Removing an original repository does not implicitly delete stored results. Show unavailable source/diff information explicitly.
- Missing or expired evidence must not be interpreted as success.
- Pruning a newer result must not make an older success become authoritative again.

### Validation, failure reports, comparison, and reruns

- `ach check --commit <sha>` evaluates the required checks for that exact commit and compatible execution context.
- Use the most recently accepted matching attempt. A newer failed or unfinished attempt prevents an older success from satisfying the gate.
- Compatibility requires matching command/dependency/report/declared-environment configuration fingerprints, OS, architecture, and shell.
- Exclude secret values from compatibility fingerprints.
- Do not claim that compatible records prove identical external tools, services, or secret values.
- All commands retain their exit status and logs.
- Parse explicitly configured JUnit XML and Go test JSON reports.
- A configured report's failures, absence, or parsing errors prevent that required check from passing, even when the command exits with zero.
- Commands without configured reports use their exit status.
- Structured failure output includes available check/test identity, relevant command, diagnostics, file/line information, and access to detailed logs.
- Leave unavailable details unknown rather than inventing locations or explanations.
- Default comparison to the immediately previous compatible execution from an earlier commit on the same repository and branch.
- Allow explicit run selection for comparison. Show unavailable or incompatible comparisons clearly.
- Distinguish resolved, continuing, and newly observed failures where report identity supports that distinction.
- Failed-check reruns use a fresh workspace with the original commit and accepted configuration.
- Include failed and blocked checks and all necessary prerequisite checks.
- Explicitly identify any compatible successful evidence inherited from the earlier attempt.
- Preserve original attempts and their logs. Do not silently rewrite a failure into a success.

### Agent workflow, inbox, and integrations

- Provide a standard skill and validate integration with Codex, Claude Code, and OpenCode.
- Also provide generic CLI and MCP documentation for other clients.
- Supply explicit agent installation/removal commands that merge product-owned skill and MCP entries, preserve unrelated configuration, create backups, and diagnose conflicts.
- Provide `ach agent-guide` so the procedure is accessible without an installed skill.
- Provide `ach mcp` as the stdio MCP entry point.
- Use a shared Go core for CLI, Connect RPC, and MCP so state interpretation does not diverge.
- Return structured, versioned results with stable statuses and error classifications.
- Keep MCP protocol output separate from diagnostic logging.
- The skill instructs agents to retain the run receipt, continue independent work while checks execute, inspect failures, and verify the final commit before claiming validation complete.
- Treat command output and failure logs as data, not as instructions to the agent.
- Support recovery after context loss through repository/commit lookup and `ach inbox --repo .`.
- Inbox includes pending work and completed results requiring acknowledgement.
- Acknowledgement is explicit through CLI, MCP, or the web. Merely viewing a result must not acknowledge it.
- All clients share the same per-run acknowledgement state.
- Acknowledgement does not change validation outcomes.
- Expiration of a CLI/MCP wait ends the wait only; it does not terminate the check.

### CLI and pre-push contract

Provide:

- `ach init`
- `ach config validate`
- `ach run`, `status`, `wait`, `logs`, `check`, `inbox`, `ack`
- `ach failures`, `plan`, `doctor`, `compare`, `rerun`, `cancel`, `ui`
- `ach hooks install`, `hooks uninstall`
- `ach agent install`, `agent uninstall`, `agent-guide`
- `ach daemon start`, `daemon status`, `daemon stop`
- `ach browser list`, `browser revoke`
- `ach mcp`
- `ach pre-push`
- `ach prune`
- `ach self-update`
- `ach version`

Behavior:

- Machine-facing operations support versioned JSON with clean stdout and separate diagnostics.
- `plan` explains selected checks, applicability, dependencies, and scheduling without executing user commands.
- `doctor` reports configuration, required tools/shells, referenced environment availability, registry/state health, and actionable recovery steps without exposing secret values.
- Exit codes:
  - `0`: successful operation; validation gates use it only for passing validation.
  - `1`: failed or incomplete validation.
  - `2`: invalid usage or configuration.
  - `3`: runner or storage error.
  - `4`: query wait expiration.
- A successful status query may return `0` while reporting a failed check in its structured result.

Pre-push:

- Install pre-push integration only when explicitly enabled; post-commit remains the default hook installation.
- Evaluate the actual object IDs in Git's pre-push input rather than assuming the current `HEAD` is being pushed.
- Require success for every branch creation/update in a multi-ref push.
- Exclude tag pushes and ref deletions.
- Provide configurable incomplete-result policies:
  - `block`, default: reject immediately and show next steps.
  - `wait`: wait for an existing unfinished execution; reject when no execution exists.
  - `run-and-wait`: create an execution when necessary and wait for its result.
- A wait or missing result must never be treated as a successful check.
- Apply the same exact-commit, latest-attempt, required-check, and compatibility rules as `ach check`.

### Web application and local API security

- Host the static application and `/docs` at https://ach.delino.io using Cloudflare Pages.
- Support desktop Chrome and Edge current stable releases.
- Provide repository/worktree navigation, branch selection, Changes, Commits, Checks, Inbox, run details, logs, structured failures, and comparison.
- Allow acknowledgement, rerunning existing executions, and cancellation.
- Keep command authoring and configuration changes in CLI/configuration files.
- For Changes, use an explicitly configured base first, then locally known `origin/HEAD`; otherwise require a base selection.
- Compare against the merge base for branch changes and display the relevant commit identities.
- Do not automatically fetch remote branches to select a diff base.
- Provide disconnected, unpaired, permission-denied, empty, loading, running, failed, cancelled, interrupted, missing-data, and incompatible-version states with actionable next steps.
- Validate keyboard navigation, focus behavior, screen-reader semantics, and status indicators that do not rely on color alone.
- Bind the API to loopback and require allowed-Origin checks and authentication for result access and control.
- Use the official application Origin and explicitly documented development Origins; do not use wildcard access.
- First connection uses a single-use pairing code expiring after five minutes.
- Keep pairing secrets out of server-visible URL query strings and operational logs.
- Remember registered browser authorization until explicitly revoked.
- Browser revocation must invalidate subsequent access.
- Never expose arbitrary shell command submission or unrestricted filesystem reads through the browser API.
- Render logs and source as inert content. Validate report/file access against the relevant owned execution data.
- Results and logs remain local; the static hosting service is not a result storage backend.

### Credentials and trust

- Support trusted, explicitly registered developer repositories.
- Isolated committed workspaces are source isolation, not a security sandbox for hostile commands.
- Supply only necessary system/tool context and explicitly declared environment inputs.
- Support environment-variable names and local secret-file references rather than committed credential values.
- Never persist the complete process environment or raw resolved credential values in run metadata.
- Redact known secret values before persisting or exposing captured logs.
- Do not claim that redaction can discover every undeclared secret embedded in arbitrary output.
- Restrict local state and credential material to the owning OS account.
- Preserve original worktrees and unrelated processes during execution, cancellation, cleanup, installation, and updates.

### Release, updates, operations, and documentation

- Produce downloadable archives for all six supported OS/architecture combinations, checksums, and Sigstore verification evidence.
- Include shell and PowerShell installers and Homebrew packaging for supported targets.
- Use a manually invoked, versioned release workflow with validation, signing, and a dry-run path.
- Ordinary CI must remain non-publishing.
- Publish the static web and installation/support documentation as part of the supported release.
- Record tested platform, shell, browser, and agent-client versions in the release compatibility documentation.
- Do not add feature flags or remote rollout controls: users explicitly select installed versions and local settings.
- Provide explicit `self-update` for direct installations only.
- Verify release authenticity and artifact integrity before replacement; back up state and support recovery after replacement failure.
- Refuse self-update while checks or server processes are active and explain how to stop them.
- For Homebrew-managed installations, provide `brew upgrade` guidance instead of replacing package-owned files.
- Reject unsupported state or API versions rather than destructively converting data or misinterpreting results.
- Provide backup, upgrade, rollback, pairing-revocation, interrupted-run, stale-process, port-conflict, cleanup, and storage-failure guidance.
- Use structured local logs with run/check identifiers, lifecycle events, outcomes, and stable diagnostic codes.
- Keep secrets and pairing credentials out of operational logs.
- Use local diagnostics, documentation, and GitHub Issues for support.
- Do not send telemetry, analytics, or automatic diagnostic uploads.

Documentation-first implementation:

- Create the project index and relevant command, app, protocol, and client contracts before runtime implementation.
- Update the docs catalog, project ownership maps, and applicable root/domain `AGENTS.md` files together.
- Document the new development command and fixed port without changing the existing root DevHud workflow.
- Keep internal architecture and repository operations in `docs/`; publish user workflows, installation, configuration, CLI/MCP/skill guidance, privacy, compatibility, and troubleshooting under `/docs`.
- Generate required `dist` output for validation but never track it, and remove generated repository-owned `dist` directories from the final worktree.

## Acceptance Criteria

- Committing submits checks durably, returns a usable receipt, and allows the Git command to finish while checks remain active.
- Both modes produce equivalent run/check results, acknowledgements, comparison behavior, and final validation decisions.
- Daemon mode uses one owner for all registered worktrees; on-demand mode leaves no idle daemon after work and temporary viewing have ended.
- Editing the developer's worktree after committing cannot change the source being checked.
- An agent can recover a run after losing context, inspect structured failures, rerun the necessary graph, acknowledge results, and verify the final commit.
- All six additional features are implemented: final check, inbox, structured failures, plan/doctor, comparison, and failed-check reruns.
- Required checks cannot pass through missing, malformed, failed, incompatible, expired, interrupted, or incomplete evidence.
- Optional-check failures remain visible without incorrectly failing the required-check gate.
- Pre-push checks every actual branch tip being pushed and obeys the configured block/wait/run-and-wait policy.
- An older success cannot override a newer matching failed or unfinished attempt.
- Queue, replacement, cancellation, and dependency semantics remain correct across concurrent worktrees and both service modes.
- Web access requires valid pairing/authentication, and revoked browsers lose access.
- The web provides the agreed read and control operations with desktop accessibility and explicit error/recovery states.
- Existing hooks, agent settings, user worktrees, unrelated processes, and package-manager ownership are preserved.
- Retention, cleanup, and updates preserve their stated ownership and evidence guarantees.
- All six platform/architecture targets pass actual integration validation before the first supported release.
- Installation artifacts, signatures, static web, compatibility information, internal contracts, and public documentation are complete.

## Test Scenarios

- Run post-commit end to end in daemon and on-demand modes; verify Git exits before a controlled long-running check finishes.
- Race simultaneous commits and daemon starts across linked worktrees; verify one daemon, durable receipts, no accidental duplicate automatic execution, and correct repository identities.
- Verify command graphs with parallel branches, dependencies, optional checks, OS exclusions, cycles, missing dependencies, and zero applicable checks.
- Exercise parallel, queue, and replace groups across worktrees and repositories, including interruption and descendant-process cleanup.
- Modify uncommitted files and switch branches while a run is active; verify exact committed source and captured configuration.
- Change configuration during execution; verify old runs remain unchanged and server configuration changes require stopping active components.
- Exercise JUnit XML and Go test JSON with passing, failing, malformed, missing, and unknown-location reports, including exit-zero commands that contain test failures.
- Verify failed/blocked rerun selection, prerequisite execution, inherited-success provenance, latest-attempt gating, and configuration/environment incompatibility.
- Test ack idempotency, shared inbox state, context-loss recovery, and the absence of implicit acknowledgement on reads.
- Test pre-push with multiple branches, explicit refspecs that differ from HEAD, no record, pending work, newer failures, force updates, tag pushes, and deletions under all three policies.
- Terminate workers or the daemon and restart; distinguish unstarted work from interrupted executions without automatically replaying interrupted commands.
- Exercise database/file failures, unavailable source repositories, retention previews, unacknowledged-result pruning, protected active data, and prevention of older-success resurrection.
- Verify temporary viewer shutdown does not stop checks, mode changes preserve history, port conflicts do not remap, and incompatible clients fail clearly.
- Test pairing-code expiry and one-time consumption, remembered browser access, revocation, disallowed Origins, unauthenticated calls, and inert rendering of hostile log/report content.
- Test missing environment references and secret redaction across streamed output boundaries without persisting complete environments.
- Install and remove hooks and agent integrations repeatedly, including existing native hooks, Lefthook, custom hook paths, and conflicting client settings.
- Validate actual Codex, Claude Code, and OpenCode CLI/MCP/skill workflows, including a wait ending without cancelling the execution.
- Verify keyboard, focus, screen-reader, and non-color status behavior in desktop Chrome and Edge.
- Test installers and self-update on supported targets, including invalid signatures/checksums, active-process refusal, interrupted replacement, state backup/recovery, and Homebrew ownership.
- Validate the manual release dry run produces the expected artifacts without publishing; require real six-target integration evidence before release.
- Run relevant Go tests and frontend `pnpm test`, protocol/client consistency checks, builds, and release-contract checks with isolated test configuration and state.

## Out of Scope

- Parsing or executing GitHub Actions workflows, act integration, or Runmoor integration.
- A required Docker execution backend or hostile-code sandbox.
- Git submodule and Git LFS execution support.
- Simultaneous use by multiple OS accounts through a shared system daemon.
- Product-imposed global command concurrency limits or command execution timeouts.
- Automatic replay of interrupted commands or reuse of failed execution workspaces.
- Per-agent acknowledgement state.
- Remote result/log storage, telemetry, analytics, automatic diagnostic uploads, and an operations backend.
- Feature flags and remote rollout management.
- OS-login autostart installation.
- Safari, Firefox, mobile applications, mobile-browser support, and non-English localization.
- Browser-based command authoring or configuration editing.
- Background automatic updates or direct replacement of Homebrew-owned binaries.
- An `async-commit-hook` executable alias.


## Owner amendment: local UI and separate public documentation (2026-09-20)

The original issue snapshot above is retained for provenance. The owner explicitly superseded hosted application/pairing requirements: ach now embeds and serves the UI beside its API, requires no pairing or persistent browser credentials, and retains on-demand viewer behavior. https://ach.delino.io becomes a documentation-only Rspress site owned by apps/async-commit-hook-docs, including the existing installer URLs. The protocol keeps Pair only as a deprecated Unimplemented tombstone. Follow the updated project, application, protocol, command and public-docs contracts for current behavior.
