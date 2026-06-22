# Troubleshooting

## No Runtime Selector Resolved

Symptom:

```text
No runtime selector resolved
```

Fix:

```bash
nodeup default lts
nodeup override set 22.1.0
```

Resolution order is explicit selector, directory override, then global default.

## Runtime Is Not Installed

Fix:

```bash
nodeup toolchain install <runtime>
nodeup run --install <runtime> node --version
```

`nodeup run` requires `--install` to install a missing runtime. Managed alias dispatch installs a missing selected version automatically.

Comparison:

| Path | Installs missing version runtime? | What to do |
| --- | --- | --- |
| `nodeup run <runtime> node --version` | No | Retry as `nodeup run --install <runtime> node --version` or run `nodeup toolchain install <runtime>` first. |
| Managed `node`, `npm`, `npx`, `yarn`, or `pnpm` shim | Yes, for version runtimes selected by default or override | Check `nodeup default` or `nodeup override list` if the wrong runtime is selected. |
| Linked runtime | No | Fix the external runtime path or relink a different runtime. |

In JSON mode, missing-runtime errors from `nodeup run` include `diagnostics.install_on_demand_eligible: false` and `diagnostics.retry_with_install`.

## Runtime Removal Is Blocked

Symptom:

```text
Cannot uninstall v22.1.0; referenced by blocking runtime selectors
```

`nodeup toolchain uninstall <version>` removes exact installed versions only. It refuses to remove a runtime while an exact-version global default or directory override still points at that runtime.

Inspect the blocking references:

```bash
nodeup --output json toolchain uninstall 22.1.0
nodeup default
nodeup override list
```

If the blocker is `global-default`, change the default first:

```bash
nodeup default <runtime>
nodeup toolchain uninstall 22.1.0
```

If the blocker is `directory-override`, remove or update the override path reported in the error:

```bash
nodeup override unset --path <path>
nodeup toolchain uninstall 22.1.0
```

```bash
nodeup override set <runtime> --path <path>
nodeup toolchain uninstall 22.1.0
```

JSON errors expose `diagnostics.blockers` with the blocker `reference_type`, `path`, `selector`, `runtime`, `clear_command`, and `change_command`.

## Command Does Not Exist

Check the active runtime and executable path:

```bash
nodeup show active-runtime
nodeup which node
nodeup which --runtime 22.1.0 npm
```

For linked runtimes, verify the runtime root contains a runnable `bin/node` or `bin/node.exe`. On Unix, `bin/node` must have an executable permission bit.

`toolchain link` reports a managed shim availability matrix for `node`, `npm`, `npx`, `yarn`, and `pnpm`. The minimum link requirement is still only runnable `node`; package-manager commands can be missing and fail later when a shim or `nodeup which <command>` needs them.

JSON missing-command diagnostics include:

- `checked_paths`
- `selected_path`
- `linked_runtime_name`
- `linked_runtime_path`
- `direct_executable_exists`
- `direct_executable_runnable`
- `install_on_demand_eligible`
- `path_precedence_guidance`

Remove a stale linked runtime record without deleting the external directory:

```bash
nodeup toolchain unlink <name>
```

If unlinking reports `conflict`, change the default runtime or remove/update the blocking directory override first.

## Shims Are Missing or Stale

Repair managed aliases:

```bash
nodeup shim setup
```

If output includes a PATH instruction, run it for the current session and add the shim directory to your shell profile or user PATH for future sessions. On Windows, Nodeup uses copied `.exe` aliases, so rerun `nodeup shim setup` after moving or replacing `nodeup.exe`.

If setup reports a conflict, review the listed path, ownership classification, and remediation. Nodeup will not replace unrelated commands; move the conflicting file or choose another shim directory with `nodeup shim setup --dir <path>`.

## Windows Shim Is Shadowed

Windows resolves commands using `PATH` order and `PATHEXT`. Nodeup shim aliases such as `npm.exe` must appear earlier than other Node.js or package-manager commands when you want Nodeup to dispatch them.

Check the effective command order:

```powershell
where npm
where node
Get-Command npm -All
```

If another `npm.cmd`, `npm.exe`, `node.exe`, or package-manager directory appears first, move the Nodeup shim directory earlier on `PATH` or invoke the desired shim by full path.

Do not confuse the shim alias file with the runtime executable Nodeup delegates to. A Nodeup shim should be a copied or linked executable such as `npm.exe`; a batch wrapper that calls `nodeup.exe` does not preserve the wrapper name as Nodeup's `argv[0]`. The selected Windows Node.js runtime usually provides package managers as `bin/npm.cmd`, `bin/npx.cmd`, `bin/yarn.cmd`, and `bin/pnpm.cmd`.

## packageManager Conflict

If `package.json` says `pnpm@10.32.1`, running `yarn` fails with `conflict`.

Fix the command or update `packageManager`:

```json
{
  "packageManager": "yarn@4.13.0"
}
```

## Invalid packageManager

Nodeup requires `<manager>@<exact-semver>` with manager `yarn` or `pnpm`.

Invalid examples:

```json
{ "packageManager": "pnpm@10.x" }
{ "packageManager": "npm@10.0.0" }
{ "packageManager": 10 }
```

How Nodeup reports them:

- `pnpm@10.x` fails on `failed_part: "version"` with `problem: "non-exact-semver"` and suggests `pnpm@<major>.<minor>.<patch>`.
- `npm@10.0.0` fails on `failed_part: "manager"` with `problem: "unsupported-manager"` because only `yarn` and `pnpm` participate in package-manager dispatch.
- `10` fails on `failed_part: "value"` with `problem: "non-string"` and reports the expected JSON string shape.

JSON errors keep `kind`, `message`, and `exit_code`, and add deterministic diagnostics such as `package_json_path`, `expected`, `supported_managers`, `failed_part`, `problem`, `correction`, and type or version details when applicable.

Corepack descriptors, ranges, tags, and `npm@...` values are not accepted. Use an exact string:

```json
{ "packageManager": "pnpm@10.32.1" }
```

## yarn or pnpm Uses npm exec

Nodeup intentionally delegates `yarn` and `pnpm` through the selected runtime's `npm exec` when a pinned `packageManager` value is present or when no direct package-manager binary exists.

Human output names the package spec and reason. JSON output includes `planning.mode`, `planning.package_spec`, `planning.package_json_path`, `planning.reason`, and `planning.package_spec_pinned`.

If `packageManager` is absent and the runtime has no direct `bin/yarn` or `bin/pnpm`, Nodeup uses an unpinned fallback:

- `yarn` -> `@yarnpkg/cli-dist`
- `pnpm` -> `pnpm`

Unpinned fallback versions can drift as the npm registry changes. Add an exact `packageManager` value for reproducible projects.

Because npm-exec mode uses npm resolution, npm registry outages, npm authentication, proxy settings, `.npmrc`, and npm cache configuration can affect `yarn` and `pnpm` dispatch. Fix the underlying npm configuration or switch to a runtime that provides a direct package-manager binary.

## Install Fails on Unsupported Host

Nodeup supports macOS x64, macOS arm64, Linux x64, Linux arm64, Windows x64, and Windows arm64 hosts. x86 hosts are unsupported.

Direct installers fail before release lookup or asset download. Runtime installation and shim dispatch fail with `unsupported-platform` before archive download or delegated command planning.

Fix: use an x64/arm64 host or a supported CI image.

JSON errors include deterministic diagnostics:

- `os`
- `architecture`
- `platform_source`
- `supported_platforms`

## Direct Installer Reports Missing cosign

Symptom:

```text
[install.nodeup] missing required prerequisite: cosign
```

Direct installers require `cosign` before release artifact download because Nodeup verifies `SHA256SUMS` entries and Sigstore bundle sidecars. This is a missing-prerequisite failure, not a signature verification failure and not a reason to disable verification.

Fix: install `cosign`, keep it on `PATH`, and rerun the installer.

```bash
brew install cosign
```

On Linux without Homebrew, follow the [Sigstore cosign installation guide](https://docs.sigstore.dev/cosign/system_config/installation/). On Windows:

```powershell
winget install sigstore.cosign
# or
scoop install cosign
```

Alternate install paths are Homebrew on macOS/Linux or `cargo binstall nodeup --no-confirm` on supported hosts with published first-party assets.

## Direct Installer Verification Fails

Symptom:

```text
[install.nodeup] Sigstore bundle verification failed
```

This means `cosign` was available, but the downloaded artifact did not verify against the published Sigstore bundle and the expected GitHub Actions release workflow identity. Retry only after confirming you are using a bundle-enabled Nodeup release from `delinoio/oss`. Do not bypass verification.

## Direct Installer Release Material Is Missing

Symptom:

```text
[install.nodeup] required release verification material is missing
```

Direct installers support bundle-enabled releases only. The selected release must include `SHA256SUMS`, the selected artifact, and the selected artifact's `<artifact>.sigstore.json` bundle sidecar. Legacy `.sig` or `.pem` sidecars are not supported by the direct installer and are not treated as equivalent verification material.

Fix: choose a newer bundle-enabled Nodeup release from `delinoio/oss`, use Homebrew on macOS/Linux when the formula points at a complete release, or use `cargo binstall nodeup --no-confirm` on supported hosts when the release includes the matching first-party asset.

## cargo-binstall Cannot Find an Asset

Nodeup's `cargo-binstall` metadata points only at first-party GitHub Release assets for macOS, Linux, and Windows x64/arm64 hosts. It disables `quick-install` and `compile`, so unsupported hosts or releases missing the matching asset fail instead of compiling from source or using third-party binaries.

Fix:

1. Confirm the host is macOS x64/arm64, Linux x64/arm64, or Windows x64/arm64.
2. Confirm the Nodeup release includes the matching `nodeup-<os>-<arch>.tar.gz` or `nodeup-windows-<arch>.zip` asset.
3. Use Homebrew on macOS/Linux or the direct installer with `cosign` when `cargo-binstall` is not the right path.

## Checksum Mismatch

Nodeup validates downloaded Node.js runtime archives against upstream `SHASUMS256.txt`.

Fix:

1. Remove the downloaded archive from the Nodeup downloads directory.
2. Retry the install.
3. If a mirror is configured, verify `NODEUP_DOWNLOAD_BASE_URL` and `NODEUP_INDEX_URL` point to matching release data.

## Stale Release Index Cache

Channel selectors such as `lts`, `current`, and `latest` can use the cached Node.js release index. If the cache is expired and refresh fails, Nodeup falls back to stale cache data instead of failing channel resolution.

Symptoms:

```text
release index: stale cache fallback
```

In JSON output, inspect `release_index.cache_state`, `release_index.fallback_reason`, `release_index.cache_age_seconds`, `release_index.selector`, and `release_index.selected_version`.

Fix:

1. Verify network access to `NODEUP_INDEX_URL` or the default Node.js release index.
2. If a mirror is configured, verify it serves valid release-index JSON for the same source URL.
3. Clear the cached index from the cache root shown by `nodeup show home`.
4. Retry with a short TTL, for example `NODEUP_RELEASE_INDEX_TTL_SECONDS=0 nodeup default lts`.

Invalid cache schema, mismatched source URL, invalid JSON, and future timestamps are ignored rather than used as stale fallback.

## Invalid Release Index TTL

`NODEUP_RELEASE_INDEX_TTL_SECONDS` must be a non-negative integer duration in seconds.

Valid examples:

```bash
NODEUP_RELEASE_INDEX_TTL_SECONDS=0 nodeup default latest
NODEUP_RELEASE_INDEX_TTL_SECONDS=300 nodeup toolchain install lts
```

Invalid examples:

```bash
NODEUP_RELEASE_INDEX_TTL_SECONDS= nodeup default lts
NODEUP_RELEASE_INDEX_TTL_SECONDS=-1 nodeup default lts
NODEUP_RELEASE_INDEX_TTL_SECONDS=abc nodeup default lts
```

Invalid values fall back to 600 seconds. Human/log diagnostics report only a safe invalid-value category such as `empty`, `negative`, or `not-integer`.

## JSON Output Has Log Noise

Keep `RUST_LOG` unset:

```bash
nodeup --output json show home
```

JSON mode disables Nodeup logging by default, but an explicit `RUST_LOG` can re-enable it.

## Colors Are Unexpected

Check precedence:

1. `--color`
2. `NODEUP_COLOR`
3. `NO_COLOR`
4. terminal detection

Inspect the effective decisions:

```bash
nodeup show color
nodeup --output json show color
```

The diagnostic separates human stdout, human stderr, and log color. Invalid `NODEUP_COLOR` and `NODEUP_LOG_COLOR` values are ignored, and the diagnostic reports the ignored value.

Force plain output:

```bash
nodeup --color never show home
NODEUP_COLOR=never nodeup show home
```

## Self Update Source Is Missing

`nodeup self update` requires `NODEUP_SELF_UPDATE_SOURCE` to point to the replacement binary:

```bash
NODEUP_SELF_UPDATE_SOURCE=/path/to/nodeup.new nodeup self update
```

Use `NODEUP_SELF_BIN_PATH` to override the target binary path.
