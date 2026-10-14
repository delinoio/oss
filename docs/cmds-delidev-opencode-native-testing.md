# DeliDev native OpenCode testing runbook

## Scope

This document owns the opt-in tests that exercise a real, user-installed OpenCode binary. Canonical paths are `cmds/delidev-cli/internal/nativetest` (shared helper), the `*_native_test.go` and `TestManualNativeOpenCode*` tests under `cmds/delidev-cli/internal/{harness,harness/opencode,server,worker}`, and `docs/cmds-delidev-harness-contract.md` for the adapter behavior these tests prove. It adds no production behavior, RPC, migration or CI job.

## Runtime and Language

Go tests only. Every native test is skipped unless `DELIDEV_NATIVE_OPENCODE_EXECUTABLE` names an installed OpenCode binary, so `go test ./...` in `cmds/delidev-cli` behaves as before with the variable unset.

## Users and Operators

Operators are developers who install or upgrade OpenCode themselves. The tests never download, install or update OpenCode and never read the user's OpenCode account, configuration or history.

## Interfaces and Contracts

### Version helper

Every native OpenCode test obtains its binary through `nativetest.NativeOpenCodeExecutable(t)`, which returns `(path, version)`. Tests must not read `DELIDEV_NATIVE_OPENCODE_EXECUTABLE` directly. The helper:

- skips when the variable is unset;
- fails the test when the path is relative;
- runs `<path> --version` in the private runtime environment (a fresh temporary `HOME`, XDG, `OPENCODE_*` updater/plugin/LSP/model-fetch opt-outs, no inherited credentials), mirroring `harness.PrivateRuntimeEnvironment` (a parity test fails if they drift);
- parses the output as one SemVer line (an optional leading `v` is accepted) and fails the test for empty or non-SemVer output or a failing process;
- logs `installed_version` and `baseline_version` (`domain.OpenCodeProtocolVersion`) with `t.Logf`;
- skips with a message naming the installed and minimum versions when `domain.HarnessVersionAllowed(domain.OpenCode, version)` is false, so the rule cannot drift from production admission.

Real-binary `ProbeConfig` values use the observed version, not `opencode.SupportedVersion`. The baseline is a minimum, not an exact pin: any admitted newer version (for example `1.18.33` or `2.0.0-rc.1`) runs the same native checks. Discovery tests assert `domain.HarnessVersionAllowed` and equality with the observed version for the real binary; the in-process fake keeps reporting the baseline.

### Install and upgrade

Install or upgrade OpenCode yourself using the official OpenCode instructions, then confirm with `opencode --version`. The version must be at or above `domain.OpenCodeProtocolVersion` (currently `1.18.32`). A lower version makes every native test skip with an explicit message; upgrade and rerun. Use the absolute path of the binary (for example `command -v opencode`).

### Commands

Run from `cmds/delidev-cli` with an absolute path:

```sh
export DELIDEV_NATIVE_OPENCODE_EXECUTABLE="$(command -v opencode)"
go test ./internal/harness -run 'TestManualNativeOpenCodeDiscovery' -count=1 -v
go test ./internal/harness/opencode -run 'NativeOpenCode' -count=1 -v -timeout 45m
go test ./internal/server -run 'NativeOpenCode' -count=1 -v -timeout 45m
go test ./internal/worker -run 'NativeOpenCode' -count=1 -v -timeout 45m
```

Use `-v` to see the logged installed and baseline versions. Narrow `-run` to one test while debugging. On Windows the same variable selects the installed Windows binary; `DELIDEV_NATIVE_OPENCODE_WINDOWS_WORKSPACE_PARENT` additionally enables the cross-drive Plan fixture.

Unit coverage for the helper itself uses fake executables and needs no OpenCode install: `go test ./internal/nativetest/... -count=1`.

### Isolation

Native tests use a generated private runtime per test: temporary `HOME`/XDG roots, temporary SQLite state, an offline npm cache, a loopback scripted provider and generated tokens. No user account, subscription, provider key, inference service, global OpenCode configuration or history is used. `requireNoManagedOpenCodeConfig` fails a fixture when a system-level managed OpenCode configuration would override the private one.

### Real-account acceptance

These tests do not establish real-account acceptance. Acceptance with a real provider account or subscription is a separate duty of the pull request that changes native behavior or raises the baseline: record it in that pull request with the OpenCode version and platform, and do not substitute scripted-provider results for it.

### Stuck-run inspection

Rerun the failing test alone with `-v -timeout 10m` and read the structured logs the fixture prints on failure (owner id, phase, installed version). If a run hangs or is interrupted, look for leftover processes with `pgrep -fl 'opencode serve'` (test servers listen on `127.0.0.1` with `--mdns=false`) and stop only those started from the test's temporary directory. Temporary roots are removed by `go test` on exit; processes are owned by an owner directory under the test's `processes` folder and are reconciled by `process.ReconcileOwner`. Never kill an OpenCode process that belongs to your own interactive session.

## Storage

All state lives in per-test temporary directories removed on test exit. Nothing is written to the repository or the user's OpenCode directories.

## Security

Credentials are generated test tokens only. Never export a real provider key while running these tests, and never paste unsanitized fixture logs that contain generated tokens into issues.

## Logging

The helper logs `installed_version` and `baseline_version` through `t.Logf`; fixtures log structured process diagnostics on failure.

## Build and Test

See Commands above. Pull request CI runs the unset-variable suite only; native runs are manual.

## Dependencies and Integrations

Depends on `internal/domain` (`OpenCodeProtocolVersion`, `HarnessVersionAllowed`). The helper package must not import `internal/harness` or `internal/harness/opencode`, because their own tests import it.

## Change Triggers

Update this document when the baseline version rule, the private runtime environment, the helper API or the set of native test packages changes.

## References

- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [DeliDev project index](project-delidev.md)
