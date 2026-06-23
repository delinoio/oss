# Runtime Resolution

Nodeup resolves a runtime selector to either an installed Node.js version or a linked runtime directory.

## Selector Forms

| Selector | Meaning |
| --- | --- |
| `22.1.0` | Exact semantic version, normalized to `v22.1.0` |
| `v22.1.0` | Exact semantic version |
| `lts` | First LTS entry from the Node.js release index |
| `current` | First entry from the Node.js release index |
| `latest` | Alias of `current`; resolves to the first entry from the Node.js release index |
| `work-node` | Linked runtime name |

Use `current` in examples and automation when you want the newest release-index entry. `latest` remains supported for compatibility and reports `canonical_selector: "current"` in JSON output.

Linked runtime names must match `[A-Za-z0-9][A-Za-z0-9_-]*`. Selector names are case-sensitive, but linked names that differ from reserved channels only by case, such as `LTS`, `Current`, or `LATEST`, are rejected to avoid confusing them with `lts`, `current`, and `latest`.

## Precedence

Runtime resolution follows this order:

1. Explicit selector from commands such as `nodeup run <runtime> ...` or `nodeup which --runtime <runtime> ...`.
2. Directory override from `nodeup override set`.
3. Global default from `nodeup default <runtime>`.

If no selector resolves, Nodeup returns a `not-found` error with a hint to set a default or directory override.

## Directory Overrides

Overrides are matched against the current working directory and its ancestors. Use them to pin a project to a runtime:

```bash
nodeup override set lts --path ~/src/service
cd ~/src/service/packages/api
nodeup show active-runtime
```

Useful override commands:

```bash
nodeup override list
nodeup override unset --path ~/src/service
nodeup override unset --nonexistent
```

`--path` removes one target. `--nonexistent` performs global stale-entry cleanup. The two flags cannot be combined.

## Global Default

The global default is used when no explicit selector or matching override exists:

```bash
nodeup default lts
nodeup default
```

When a saved default no longer resolves, `nodeup default` still reports the saved selector and includes a resolution error in JSON output.

Setting a default can install a version/channel target as a side effect. Human output reports the resolved runtime as `installed` or `already-installed`, and JSON output includes `install_side_effect` with the runtime, status, and whether the default command performed a fresh install.

JSON selector-bearing responses include:

- `selector_kind`: `exact-version`, `channel`, or `linked-runtime`
- `canonical_selector`: the semantic selector identity used for tracking and alias reporting
- `selector_alias_of`: present when a selector is an alias, currently `latest` -> `current`

## Installed and Linked Targets

Exact versions and channels resolve to version directories under the toolchains root. Linked names resolve to the registered path.

Linked runtime records are registered with `nodeup toolchain link <name> <path>` and removed with `nodeup toolchain unlink <name>`. Unlinking removes only the nodeup settings record and tracked selector; it does not delete the external runtime directory.

Nodeup verifies availability when commands need an executable:

- `nodeup show active-runtime` requires runnable `node`.
- `nodeup which <command>` requires the resolved direct executable to exist and be runnable.
- `nodeup run <runtime> <command>` requires the resolved direct executable to exist and be runnable.
- Managed alias dispatch installs a missing selected version before execution.

For linked runtimes, Unix hosts require an executable bit on `bin/node`. Windows platform behavior resolves `node` to `bin/node.exe`.

For platform override tests, `NODEUP_FORCE_PLATFORM` accepts macOS aliases in either documented host spelling (`macos-x64`, `macos-arm64`, `macos/x64`, `macos/arm64`) or runtime archive spelling (`darwin-x64`, `darwin-arm64`).

`toolchain link` only requires the linked runtime to provide runnable `node`. Package-manager commands are optional and are checked per command later. Successful link output reports the required `node` check separately from optional availability for each managed shim command:

| Shim command | Linked-runtime direct path on Unix | Linked-runtime direct path on Windows |
| --- | --- | --- |
| `node` | `bin/node` | `bin/node.exe` |
| `npm` | `bin/npm` | `bin/npm.cmd` |
| `npx` | `bin/npx` | `bin/npx.cmd` |
| `yarn` | `bin/yarn` | `bin/yarn.cmd` |
| `pnpm` | `bin/pnpm` | `bin/pnpm.cmd` |

Missing linked-runtime commands fail when resolved by `which`, `run`, or a managed shim. JSON errors include the linked runtime name, checked paths, selected path, direct executable state, install-on-demand eligibility, and PATH/PATHEXT precedence guidance.

## Missing Runtime Installation Behavior

`nodeup run` and managed shim dispatch intentionally differ when a selected version runtime is missing:

| Execution path | Selector source | Missing version runtime behavior | JSON diagnostic |
| --- | --- | --- | --- |
| `nodeup run <runtime> <command>` | Explicit argument | Fails unless `--install` is provided. | `install_on_demand_eligible: false` plus `retry_with_install`. |
| Managed shim alias such as `node` or `npm` | Directory override or global default | Installs the missing selected version runtime before execution. | `install_on_demand_eligible: true` for version runtimes. |
| Linked runtime selector | Explicit, override, or default | Never installed by Nodeup; linked paths are external. | `install_on_demand_eligible: false` and linked runtime fields. |

Use `nodeup run --install <runtime> <command>` when explicit runtime execution should provision a missing version. Use managed aliases when the active default or override should behave like a rustup-style shim.

## Release Index Cache

Channel selectors use the Node.js release index. The cache:

- defaults to 600 seconds
- is stored under the cache root
- is ignored when its schema, source URL, or timestamp is invalid
- can fall back to stale cache entries after refresh failures

Set `NODEUP_RELEASE_INDEX_TTL_SECONDS` to tune the TTL:

```bash
NODEUP_RELEASE_INDEX_TTL_SECONDS=300 nodeup default lts
NODEUP_RELEASE_INDEX_TTL_SECONDS=0 nodeup --output json which --runtime latest node
```

The value must be a non-negative integer duration in seconds. Empty values, negative values such as `-1`, and non-integer values such as `abc` are invalid. Invalid values produce a safe warning category in human/log diagnostics and keep the 600-second fallback TTL. JSON mode keeps stdout machine-parseable.

When a channel selector uses stale cache because refresh failed, JSON responses for channel-resolving commands include `release_index`:

```json
{
  "runtime": "v22.11.0",
  "release_index": {
    "cache_state": "stale-fallback",
    "fallback_reason": "refresh-failed",
    "cache_age_seconds": 3600,
    "ttl_seconds": 600,
    "selector": "lts",
    "selected_version": "v22.11.0",
    "source_url": "https://nodejs.org/download/release/index.json"
  }
}
```

The `source_url` field is sanitized and omits query strings and fragments.
