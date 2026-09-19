# Configuration

Direct argv execution works without configuration. Named commands use `runlens.toml` in the repository root (or current directory outside Git). Select a different file explicitly with `--config <path>`.

```toml
schema_version = 1
exclusions = ["large-local-data/**"]

[commands.build]
argv = ["your-build-tool", "build"]
cwd = "."
inputs = ["src/**", "build.toml"]
outputs = ["out/**"]
prepare = [["your-build-tool", "prepare"]]
env = ["CI"]
timeout_ms = 120000

[policy]
deny_reads = ["private/**"]
deny_writes = ["src/**"]
require_inputs = false
require_outputs = false
fail_new_accesses = false

[limits]
memory_bytes = 268435456
total_bytes = 1073741824
max_paths = 1000000

[redaction]
patterns = ["customer-[0-9]+"]
environment_names = ["MY_SECRET"]
argument_indices = [3]
```

The example tool is a placeholder; select an installed executable appropriate for your platform. On macOS, protected system interpreters cannot be replaced: use an explicitly installed injectable executable.

`argv` is an array, not shell source. To interpret shell syntax, explicitly specify the shell executable and its command argument. Loading this file does not execute `argv` or `prepare`.

`cwd` is relative to the workspace root and must remain inside it. Input/output/exclusion patterns use forward slashes and glob syntax; `**` crosses directory boundaries. Exclusions narrow snapshot coverage and are visible in the report. Git-ignored paths are otherwise included.

Preparation applies only to clean/repeated verification, in the listed order. A failed or incompletely observed preparation prevents target execution. Runlens never infers installation steps. Environment selections contain **names only**, never values. Ordinary `run` inherits its calling environment; clean verification uses required OS context, fresh HOME/cache locations, and selected names only. Reserved isolation variables cannot replace the temporary directories.

## Policy boundaries

`allow_reads` and `allow_writes`, when supplied, are explicit allowlists. `deny_reads` and `deny_writes` reject matching observations; deny rules take precedence. Policies also inspect snapshot-proven changes, including writes the access backend did not report.

Use normalized external roots such as `${home}/tools/**` when explicitly permitting external accesses. Environment variable reads and network use are not filesystem observations.

```sh
runlens policy check build.json --baseline baseline.json --json
runlens cache check build.json --command build --json
```

`fail_new_accesses = true` requires `--baseline`. Input/output coverage policies require a report associated with a matching named command. Unknown or incomplete evidence cannot pass a check. Cache audits describe supplied observations; they never certify universal cache safety.

Schema version 1 rejects unknown fields, malformed patterns, unsupported versions, and invalid limits with an input error. See [reports](/reports) for compatibility and exit codes.
