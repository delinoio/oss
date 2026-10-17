# TypeNative SDK and Developer Workflow

## Scope

Own `packages/typenative-cli/` and the planned `packages/typenative/` SDK and React renderer. This contract preserves the complete framework requirements across the native host boundary.

## Runtime and Language

Use TypeScript for SDK, React renderer and CLI; use Node.js 24 for CLI/build tools only. Compile application TypeScript with ScriptC. Use Rust for the Blitz host. Do not embed a JavaScript engine in development or distribution applications. Native compilation is outside the Rspack browser-bundle workflow.

## Users and Operators

TypeScript application authors, trusted native-plugin authors and framework maintainers.

## Interfaces and Contracts

**Project structure and implementation**

- Use project ID `typenative`.
- Implement the SDK, React renderer, and CLI in TypeScript; implement the Blitz host and native FFI in Rust, with required platform integration code.
- Create the project index and owning domain contracts before runtime implementation. Record the TypeScript/Rust choice as a project-specific exception to the repository language default.
- Permit revision-pinned upstream patches or forks. Document their cause, scope, removal conditions, and original licenses.
- Preserve existing projects and repository workflows.

**Public packages and developer workflow**

- Publish `@delino/typenative-cli` and `@delino/typenative` separately as compatible 0.x experimental releases.
- Install the CLI as `tn`, with `create`, `dev`, `build`, `run`, and `doctor` commands.
- Support project creation, native development execution, target builds, development package generation, installation, and launch.
- Require explicit preparation of toolchains. Developers install platform prerequisites; `doctor` reports missing or incompatible tools.
- Pin compatible framework, compiler, renderer, and plugin versions. Do not automatically upgrade them.
- Check distribution integrity. Fail on mismatches or incomplete installation.
- Roll back through an explicitly selected previous compatible version combination without deleting application data.

**Application command target selection**

The application pipeline uses `tn dev [--target <target>]`, `tn build [--target <target>]`, and `tn run [--target <target>]` from the project root containing `typenative.json`. Each command accepts `--json` for structured diagnostics. These command forms describe the required application pipeline; the initial unavailable-pipeline behavior below remains in force until that pipeline exists.

- An explicit `--target` selects exactly one canonical target from the project index. Reject empty, unknown, repeated, or multiple target values and unexpected positional arguments with an argument diagnostic and nonzero exit status before invoking the pipeline.
- Without `--target`, select the current desktop host/architecture combination. Never infer a mobile target, select an attached device automatically, or fall back to a different architecture. If the host has no canonical desktop target, require an explicit target.
- `build` creates the selected target's development package only when the host toolchain supports it. Reject unsupported host/target combinations before compiling; do not silently build the host target instead.
- `dev` and `run` execute desktop applications only on the matching host target. Mobile execution requires explicit `ios-arm64-simulator` or `android-arm64-emulator` and a compatible prepared virtual device. If no compatible device exists, or more than one is available, fail with an actionable diagnostic rather than guessing. The user must leave exactly one compatible virtual device available.
- `dev` compiles and launches the selected target with the update behavior below. `run` installs and launches the selected target's development package; reject a missing package or mismatched target. `build` does not launch an application. Never use cross-architecture emulation as target acceptance.

**React and native development updates**

- Support the complete stable public client-side React 19.3.1 core API.
- Preserve the application process and compatible React state when replacing compiled component code.
- Changes that invalidate component identity or Hook structure, and native host/plugin changes, may restart the application with an explicit explanation.
- Do not substitute snapshot-and-restart behavior for compatible updates.
- On compilation failure, retain the last successful running application and its state. Report diagnostic codes and source locations. If no successful build exists, do not launch an application.
- Add required compiler/runtime support when upstream capabilities are insufficient.

**Rendering and host behavior**

- Support text, images, buttons, inputs, selection controls, scrolling, flex and grid layouts, basic box styling, CSS variables, and media queries.
- Document supported HTML, CSS, and event behavior. Report statically detectable unsupported use through build diagnostics and dynamically detected use through development logs.
- Support one desktop window and one mobile screen host, resizing, close handling, foreground/background transitions, rotation, safe areas, and the virtual keyboard.
- Validate keyboard navigation, focus, accessibility semantics, screen-reader behavior, and Korean IME input.
- Do not promise general compatibility with React DOM libraries.

**Platform acceptance**

- Desktop: macOS arm64, Windows x64, and Linux x64.
- Mobile: iOS arm64 simulator and Android arm64 emulator.
- Derive minimum OS requirements from pinned dependencies and SDK requirements. Publish only the combinations actually validated.
- Require development installation and execution. Physical-device acceptance, distribution signing automation, and store submission are outside this MVP.

**Application APIs and state**

- Provide HTTP(S) through `fetch` and `AbortSignal`. Preserve certificate validation. Applications own authentication and retry policy.
- Provide asynchronous JSON key/value storage and file operations in an application-specific directory.
- Support explicit reads, writes, and deletion; include key/value `get`, `set`, `remove`, and `clear`.
- Reject paths that escape the application storage boundary. Make individual key writes atomic.
- Serialize writes to the same key or path within one application process; preserve the last successful write.
- Preserve stored data across application restarts. Do not add automatic expiry, encryption, cloud synchronization, or multi-process mutation guarantees.
- Provide clipboard access and external HTTP(S) URL opening. Custom URL schemes and additional OS functions belong to trusted plugins.
- Honor OS permissions and sandbox boundaries. Return explicit permission failures.

**Plugins**

- Statically include developer-selected, trusted native plugins with the application's privileges.
- Generate TypeScript and Rust bindings from typed method, event, data, and error definitions.
- Support asynchronous calls, cancellation, and removable event subscriptions.
- Return errors with codes and messages; do not retry automatically.
- Release resources on cancellation, subscription removal, and shutdown.
- Do not promise native crash isolation. Secret storage can be implemented by a separate trusted plugin.

**Diagnostics, operations, and documentation**

- Produce local structured diagnostics containing versions, build identifiers, target, phase, duration, error code, and source location.
- Exclude application data, tokens, HTTP bodies, and user input from default logs.
- Do not automatically transmit diagnostics or persist log files. Users may explicitly save and sanitize diagnostic information for support.
- Record fatal errors and terminate; return recoverable API errors to the application.
- Measure startup time, memory, distribution size, UI update time, and development update time.
- Use version selection instead of remote feature flags. No remote rollout service is required.
- Provide English CLI messages, package READMEs, repository guides, API and plugin documentation, compatibility tables, troubleshooting, and change/migration notes.
- Use GitHub issues for support. Defer a public documentation website.
- Keep internal contracts and operational details in `docs/`; keep public package documentation focused on supported user workflows.

### CLI foundation boundary

The initial CLI must distinguish implemented commands from planned commands. `tn create <directory>` creates a non-overwriting React source scaffold with exact version selections. `tn doctor [--target <target>] [--scriptc <executable>] [--probe]` reports prerequisite checks. An explicitly requested probe compiles and executes a fixed non-React native program only on the current desktop target. It uses a temporary directory and removes its own artifacts. A successful probe does not establish React, renderer, plugin or platform acceptance.

Until the native renderer pipeline exists, `tn dev`, `tn build` and `tn run` must return a stable unsupported-pipeline diagnostic and fail. Do not execute a JavaScript application or present a successful console-only build as a framework application. Keep unreleased packages private until release requirements are satisfied. A scaffold can pin a future SDK version without claiming that it can currently be installed.

## Storage

Application data belongs to an application-specific local directory and survives restart and explicit rollback. Key writes must be atomic and same-key/path writes serialized within a process. Reject escaping paths. No automatic expiry, encryption, synchronization or multi-process guarantees. Tool diagnostics must not persist automatically. Scaffolding must not overwrite or remove pre-existing content.

## Security

Preserve TLS verification, OS permission and sandbox boundaries. Plugins are trusted static native code with application privileges, without crash isolation. Check distribution integrity and fail on mismatches or incomplete installations. Tool invocation must pass literal arguments without a shell. Exclude application data, credentials, bodies and user input from default logs.

## Logging

Local structured diagnostics include component versions, build identifier, target, phase, duration, error code and source location when available. Use English messages. Report unsupported source statically through build diagnostics and dynamic unsupported use through development logs. No automatic telemetry or persisted log files. Fatal errors terminate; recoverable API errors return to the application. CLI diagnostics use ANSI color by default with `NO_COLOR` opt-out; machine JSON remains uncolored.

## Build and Test

- A developer can install the npm packages, inspect prerequisites, create an application, develop it, build it, and run the development package on every agreed platform.
- All stable public client-side React 19.3.1 core APIs pass native conformance tests.
- Development and distribution applications contain no embedded JavaScript engine.
- A compatible component change preserves the process ID and React state.
- Incompatible changes explain their restart behavior; failed compilation leaves the last successful application and state intact.
- Supported rendering, input, accessibility, lifecycle, storage, networking, clipboard, URL, and plugin behavior passes the agreed platform tests.
- Unsupported functionality produces the specified diagnostics without being advertised as supported.
- Permission denial, cancellation, storage errors, network errors, and plugin failures produce observable, documented results.
- Package integrity, version compatibility, clean installation, and explicit rollback are verified without deleting application data.
- Documentation distinguishes virtual-device results from physical-device validation and identifies unsupported combinations.
- Performance measurements and validation records include source revisions, commands, results, and unperformed checks.
- Upstream blockers remain visible and prevent completion of affected requirements.

### Test scenarios

- Install published-package candidates in clean consumers and run the complete CLI workflow.
- Verify React API behavior, state scheduling, effects, cleanup, refs, context, and error handling in native builds.
- Verify repeated native updates, unchanged process identity, state preservation, incompatible-change restarts, compilation failures, and subsequent recovery.
- Verify supported HTML/CSS, events, scrolling, focus, screen readers, Korean composition, resizing, rotation, safe areas, and virtual keyboards.
- Verify storage persistence, explicit deletion, path escape rejection, ordered writes, disk failures, and application shutdown.
- Verify HTTP success, offline failure, certificate rejection, cancellation, and the absence of automatic retries.
- Verify plugin binding validation, version mismatches, calls, events, cancellation, unsubscribe, cleanup, and error propagation.
- Verify clipboard and URL operations, unsupported schemes, and OS permission failures.
- Inspect development and distribution dependencies and artifacts for JavaScript engine absence.
- Verify malformed configuration, missing toolchains, unsupported targets, corrupted artifacts, interrupted installation, and rollback.
- Verify diagnostic privacy and the absence of automatic telemetry.
- Run repository-required formatting, relevant TypeScript tests, and root `cargo test` after Rust changes. Generate required assets before validation and remove generated repository-owned `dist` directories afterward.
- Preserve affected CI selection and the prohibition on macOS runners for PRs, main pushes, and affected manual CI. Record macOS/iOS acceptance through permitted local or owning release validation.

Run `pnpm --filter @delino/typenative-cli test` and `pnpm --filter @delino/typenative-cli build` for CLI changes. Use `tn doctor --probe` separately for a real compiler execution check. Record failed, skipped and unperformed checks explicitly. Unit fixtures that emulate tool output do not count as native acceptance.

## Dependencies and Integrations

Initial investigation pins: React `19.3.1`; ScriptC `0.3.5` and reviewed source revision `d8c2b0f96d8de8fadf4937d93a701dfd3a10f4df`; Blitz revision `1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01`. These selections are inputs for compatibility work, not an assertion that the combination passes acceptance. Do not automatically upgrade them. Preserve original licenses in distributions.

### Out of scope

- React DOM compatibility guarantees, server-only React APIs, RSC, experimental React APIs, SSR, and web deployment.
- Embedded JavaScript engines in application development or distribution.
- Additional CPU combinations and physical mobile-device acceptance.
- Store submission and distribution signing automation.
- Multiple windows, tray integration, and background jobs.
- Built-in accounts, hosted services, billing, remote feature flags, app automatic updates, and remote code download.
- Automatic data expiry, encrypted storage, cloud synchronization, and multi-process storage guarantees.
- Plugin sandboxing, native crash isolation, automatic retries, and automatic runtime recovery.
- Remote telemetry, centralized dashboards, and numerical performance thresholds.
- Public documentation website and non-English CLI/documentation.

## Change Triggers

Update the [project index](project-typenative.md) and [native host contract](crates-typenative-foundation.md) with changes to the ABI, package compatibility, platform boundaries or lifecycle. Update applicable AGENTS files only for ownership or development-rule changes.

## References

- [TypeNative project index](project-typenative.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Dependency security](repository-dependency-security-contract.md)
