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

## Command Does Not Exist

Check the active runtime and executable path:

```bash
nodeup show active-runtime
nodeup which node
nodeup which --runtime 22.1.0 npm
```

For linked runtimes, verify the runtime root contains `bin/node` or `bin/node.exe`.

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

## Install Fails on Unsupported Host

Nodeup supports macOS, Linux, and Windows x64/arm64 hosts. x86 hosts are unsupported.

For local platform testing, maintainers can use `NODEUP_FORCE_PLATFORM` with values such as `linux-arm64`, `windows-x64`, or `windows-arm64`.

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

Keep `RUST_LOG` unset or off:

```bash
RUST_LOG=off nodeup --output json show home
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

## Validation Commands

Runtime crate validation:

```bash
cargo test -p nodeup
```

Documentation app validation:

```bash
pnpm --filter nodeup-docs test
```
