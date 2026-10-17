# TypeNative CLI (unreleased)

TypeNative is an experimental React and TypeScript native framework under development. This package currently provides source scaffolding and selected prerequisite checks. It is private and is not an installable public release.

## Available commands

- `tn create <directory>` creates a React component and exact version configuration in a new directory. The parent must exist. Existing directories, files and symlinks are rejected. A failed write leaves the partial directory for inspection.
- `tn doctor [--target <target>] [--scriptc <executable>]` checks the selected compiler and tools without installing or upgrading them.
- `tn doctor --probe` also compiles and executes a fixed console program with ScriptC on the current desktop target. It deletes its temporary files. This does not test the generated React component.
- Add `--json` for structured, uncolored diagnostics. Text diagnostics use ANSI color; set `NO_COLOR` to disable it.

Use Node.js 24 or later for this CLI. Select ScriptC 0.3.5 explicitly. Supply `--scriptc` when the compiler is outside PATH. Install Rust/Cargo and the target SDK separately. Doctor checks selected commands; it does not certify SDK completeness, signing, runtime images or application compatibility. Each tool invocation has a 120-second timeout. Native probe compilation and execution are asynchronous so SIGINT/SIGTERM can stop the child and await temporary-file cleanup. POSIX interruption also stops the child process group.

The target identifiers are `macos-arm64`, `windows-x64`, `linux-x64`, `ios-arm64-simulator` and `android-arm64-emulator`. Only a matching desktop host can run the console probe. Mobile and cross-target prerequisite checks do not execute an application.

## Availability and limitations

`tn dev`, `tn build` and `tn run` fail with `TN_NATIVE_PIPELINE_UNAVAILABLE`. The React renderer, Blitz host, native state-preserving updates, application services, plugins and platform packages are not implemented. The SDK is not published. The generated component is a source scaffold, not a runnable native app. No platform has TypeNative acceptance from these checks.

The selected versions are framework `0.0.0-dev.0`, React `19.3.1`, ScriptC `0.3.5` and Blitz revision `1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01`. They are compatibility inputs, not a validated combination. No command upgrades them automatically.

## Diagnostics and troubleshooting

Diagnostics include versions, an invocation identifier, target, phase, elapsed time, code and source location (`null` for these CLI-level checks). The invocation identifier is transient, not a persisted entity. No logs are saved or transmitted automatically. Default diagnostics exclude raw tool output, application content and user-supplied paths. A compiler error in the fixed probe returns a CLI-level code; it is not an application compiler diagnostic adapter.

| Code                             | Action                                                                                   |
| -------------------------------- | ---------------------------------------------------------------------------------------- |
| `TN_ARGUMENTS`                   | Check `tn --help` and the documented target/name format.                                 |
| `TN_CREATE_FAILED`               | Select a new writable directory; inspect any partial scaffold.                           |
| `TN_TOOL_UNAVAILABLE`            | Install/configure the named prerequisite and rerun doctor.                               |
| `TN_VERSION_MISMATCH`            | Select ScriptC 0.3.5 explicitly.                                                         |
| `TN_HOST_UNSUPPORTED`            | Use the matching desktop target for a probe.                                             |
| `TN_PROBE_COMPILE_FAILED`        | Check compiler installation, linker and SDK.                                             |
| `TN_PROBE_ARTIFACT_INVALID`      | Reinstall the selected compiler; the output is absent or not a native executable header. |
| `TN_PROBE_INTERRUPTED`           | The probe was interrupted; rerun it when ready.                                          |
| `TN_PROBE_EXECUTION_FAILED`      | Check host execution permissions and toolchain compatibility.                            |
| `TN_NATIVE_PIPELINE_UNAVAILABLE` | Native application execution is not implemented.                                         |
| `TN_INTERNAL`                    | Report a sanitized reproduction through GitHub issues.                                   |

The probe artifact digest identifies the bytes tested. It does not verify a framework distribution or prove the absence of embedded engines. Distribution integrity, rollback, React conformance, native rendering, accessibility and all platform acceptance remain outstanding.

## Native React development check

Maintainers can run `pnpm test:native` from a source checkout after installing development dependencies. Set `TN_SCRIPTC` to the selected ScriptC executable when it is not in PATH. This explicit check builds and runs a fixed React core fixture using `--npm-static react`; it never uses Node.js to execute the fixture. It verifies element creation, validation, cloning, child counting and ref initialization only.

Compilation diagnostics or execution failures cause a nonzero result, including known upstream failures. The JSON result reports diagnostic codes and fixture locations without raw tool output. A pass is not full React conformance, renderer acceptance or proof of engine absence. The regular unit suite does not run this check and cannot establish native compatibility.

## Changes and support

This initial development version introduces scaffolding and prerequisite diagnostics only. There is no migration or rollback workflow for applications yet. Use [GitHub issues](https://github.com/delinoio/oss/issues) for support and remove sensitive content from reports.

Repository-owned package content is Apache-2.0; see LICENSE and NOTICE.

On Windows, the pinned npm ScriptC shim resolves to its installed native `bin/scriptc.exe`. The CLI invokes that executable directly without a command shell. A missing or mismatched npm installation must be repaired before running the probe.
