# Runtime Resolution

Nodeup resolves a runtime selector to either an installed Node.js version or a linked runtime directory.

## Selector Forms

| Selector | Meaning |
| --- | --- |
| `22.1.0` | Exact semantic version, normalized to `v22.1.0` |
| `v22.1.0` | Exact semantic version |
| `lts` | First LTS entry from the Node.js release index |
| `current` | First entry from the Node.js release index |
| `latest` | First entry from the Node.js release index |
| `work-node` | Linked runtime name |

Linked runtime names must match `[A-Za-z0-9][A-Za-z0-9_-]*`. Reserved channel names are exact lowercase values.

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

## Global Default

The global default is used when no explicit selector or matching override exists:

```bash
nodeup default lts
nodeup default
```

When a saved default no longer resolves, `nodeup default` still reports the saved selector and includes a resolution error in JSON output.

## Installed and Linked Targets

Exact versions and channels resolve to version directories under the toolchains root. Linked names resolve to the registered path.

Nodeup verifies availability when commands need an executable:

- `nodeup show active-runtime` requires `node`.
- `nodeup which <command>` requires the resolved direct executable to exist.
- `nodeup run <runtime> <command>` requires the resolved direct executable to exist.
- Managed alias dispatch installs a missing selected version before execution.

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
