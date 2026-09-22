# Configuration

## Committed project configuration

Store project configuration at `.config/async-commit-hook.toml`. Commands always use the selected commit's configuration, not later working-tree edits.

```
version = 1
pre_push = "block"
diff_base = "origin/main"

[checks.prepare]
command = "pnpm install --frozen-lockfile"

[checks.test]
command = "pnpm test"
depends_on = ["prepare"]
policy = "queue"
# group = "shared-test-resource"
# optional = true
# os = ["darwin", "linux"]
# shell = "bash"

[checks.go]
command = "go test -json ./... > go-test.json"
[[checks.go.reports]]
kind = "go-test-json"
path = "go-test.json"

# Environment values are resolved locally, never committed here.
[[checks.test.environment]]
name = "TEST_TOKEN"
secret = true
credential = "test-service"
required = true
```

Shell values are `sh`, `bash`, `powershell` and `pwsh`; defaults are sh on macOS/Linux and PowerShell on Windows. OS values are darwin, linux and windows. Each report has kind `junit` or `go-test-json` and a workspace-relative output path owned by exactly one check (case-insensitively). Previous files at that path are removed before the check starts, so its command must create fresh reports. Shells and tools must be installed by the user.

Applicable checks are required unless optional=true. Dependencies share the isolated run workspace. A failed dependency blocks dependents while independent checks continue. Use named preparation commands for dependencies. Every prerequisite must apply on each OS where its dependent runs; an empty OS list means all supported systems. Cycles, missing dependencies, unsupported versions and invalid declarations fail validation.

Scheduling defaults to parallel. Queue serializes eligible work in a group; replace cancels an older check before the replacement starts. The default group is repository plus check name. Explicit groups can coordinate different repositories. There is no product-wide concurrency cap or command execution timeout.

## Personal configuration

Store machine configuration at `~/.config/async-commit-hook/config.toml`. The default state directory on every OS is `~/.local/share/async-commit-hook`.

```
version = 1
mode = "daemon" # or "on-demand"
api_port = 46309
# state_dir = "/absolute/path/to/private-state"

[credentials.test-service]
env = "LOCAL_TEST_TOKEN"
# Instead of env, use file = "/absolute/path/to/private-token-file"
# File credentials must be regular files, not symlinks, of at most 64 KiB.

[retention]
max_age_days = 0 # 0 means unlimited; maximum 106751 days
max_bytes = 0
```

Stop checks and server processes before changing mode, api_port, state_dir or credential references. Use the original configuration to stop existing processes before applying these changes. Mode changes preserve records when the same state directory is used. Stop before relocating state and copy the complete directory to the new location. `ACH_CONFIG` or `--config PATH` selects another personal configuration for isolated environments. Retain one authoritative configuration for your OS account. Existing state directories must have account-only access (mode 0700 on macOS/Linux or a protected current-user-only full-access ACL on Windows). Startup preserves existing permissions and rejects unsuitable directories. Choose a new dedicated state path or correct permissions yourself before retrying.

Only necessary system/tool context and explicitly declared inputs reach commands. Public declared environment values participate in compatibility; secret values do not. Changing only `pre_push` or `diff_base` preserves execution compatibility and comparisons; commands, dependencies, reports, declared inputs and execution platform still determine compatibility. All checks must agree whether an environment name is secret; names are compared case-insensitively for portability. Conflicting declarations are rejected before acceptance. `ACH_MANAGED` is reserved for managed workspace recursion protection and cannot be declared as an input, regardless of case. Prefer local secret-file references for queued work and daemon restarts. An environment-backed secret must be available to the worker/server process; start/restart ach from the environment that supplies it.

```
ach config validate
ach plan --commit HEAD
ach doctor
```
