# Runmoor macOS Host Runners

**Availability:** host execution is unreleased. Check your installed version and
release notes before using `runmoor init --backend host`.

The opt-in `host` backend runs separate, single-job GitHub Actions runner processes
on one **macOS 14+ Apple Silicon** computer. It requires no Docker, Tart or Guest
Agent. Runmoor's normal default on macOS remains Tart; select host explicitly.
Runmoor never falls back to host execution when another backend fails.

**Verification limits:** automated contract tests, mocked execution and builds
cover this backend. Actual Mac host execution, unsigned Xcode builds and live
GitHub jobs have not been validated. Existing Docker, Tart and package checks do
not prove host execution compatibility.

## Set up and route jobs

Use a dedicated macOS CI account. Install Xcode, required SDKs and language tools
under that account, and complete their normal license/setup steps yourself.
Runmoor does not create accounts, install tools or require a privileged helper.
Ordinary build/test workflows and unsigned Xcode builds are intended workloads.

Create a new configuration with an existing GitHub credential reference:

```sh
runmoor init --backend host \
  --target https://github.com/OWNER/REPOSITORY \
  --credential-env RUNMOOR_PAT
runmoor config validate
runmoor run
```

Supply the credential separately as described in [configuration](./configuration).
Repository and organization targets support the existing PAT and GitHub App
options. GitHub repository access, runner groups and fork-execution policies
remain authoritative. Never grant untrusted workflows access to this account.

A newly generated pool is named `macos-host`. Its scale set is
`runmoor-macos-host`, with labels `runmoor-macos-host`, `macOS` and `ARM64`:

```yaml
runs-on: [runmoor-macos-host, macOS, ARM64]
```

Existing and manually authored labels are preserved. A minimal host pool can use:

```toml
[[pools]]
name = "macos-host"
connection = "project"
scale_set = "runmoor-macos-host"
labels = ["runmoor-macos-host", "macOS", "ARM64"]
backend = "host"
mode = "plain"
```

The connection must be defined separately. Host pools reject `image`,
`image_source`, `runner_path`, DinD settings and image-only initialization.
Runmoor owns execution paths. `image` commands remain Tart operations.

## Admission and runner updates

Omitted host allocations use **2 CPUs and 4096 MiB** per runner. On smaller
budgets they shrink to available capacity, with minimums of **1 CPU and 1024 MiB**.
Explicit values are never silently reduced. Omitted concurrency follows both CPU
and memory budgets. Host execution does not inherit Tart's two-VM ceiling;
more than two runners can be admitted when shared capacity permits.

CPU and memory reservations control admission; they do **not** enforce limits on
host process usage. Host jobs share the global budget with Docker jobs, Tart VMs
and runner preparation. Minimum idle defaults to zero. Free-disk reserve defaults
to 10240 MiB, or 20480 MiB when Tart is also configured. Low disk blocks new work
without evicting active jobs.

The host preparation timeout defaults to five minutes:

```toml
[timeouts]
host_preparation = "5m"
job = "6h"
```

Idle runners do not consume their job timeout. The job timeout starts with
assignment; if removal finds a busy job whose start is unknown, Runmoor uses the
runner creation time as a conservative bound.

Runmoor selects official macOS arm64 runner releases, verifies their SHA-256 and
archive contents, and prepares an immutable distribution. Omitted or `latest`
runner versions follow managed updates; exact versions remain pins. Existing
jobs retain their original verified distribution while a replacement prepares.
Use `runmoor runner update --pool macos-host` for an immediate check and
`runmoor config show --resolved` for the committed version. Known support expiry
blocks new acquisition; it does not rerun failed jobs.

## Disposable directories and trust

Every execution receives a fresh registration, runner installation, HOME,
temporary directory and work directory. It handles at most one job. PATH and
DEVELOPER_DIR are inherited when set. HOME, TMPDIR, TMP and TEMP point to the
execution's disposable directories. Other manager environment variables and
management credentials are excluded from job launch configuration.

**Separate directories are not a security boundary.** The manager and jobs run
under the same macOS account. A job can access account files, personal data and
Keychains available to that account, including another job's resources. Support
is limited to trusted developers and small-team workflows.

Shared caches, signing Keychains, Simulator lifecycle, GUI automation and global
tool settings are workflow/operator responsibilities. Runmoor does not isolate or
manage those resources. Jobs that daemonize or detach outside the managed process
group are unsupported; their lifecycle remains the operator's responsibility.

## Cleanup and recovery

Use the normal [control commands](./commands): pause, resume, reload, drain, stop
and pool-scoped force-stop. The [launchd user service](./operations) uses the same
lifecycle. A manager-only restart reconciles verified surviving work. Normal
completion, cancellation and timeout terminate only the verified execution group
and its children before removing execution directories.

Missing supervisors, changed process identity, replaced directories and uncertain
termination keep reservations and recovery diagnostics. `stop --force` does not
authorize deleting foreign resources or signalling unrelated same-account
processes. Preserve the installation's state/data and uncertain resources; inspect
the reported safe error code and restore a matching paired backup when available.
Do not repair ownership markers or delete a directory based only on its name.

Drain and stop before a binary upgrade or rollback. Keep paired state/data backups
and wait for cleanup to finish before copying them. Restoring both together with a
compatible binary preserves verified host distributions. Do not edit ownership
markers or restore an older backup while later resources remain active.
An older binary cannot safely open newer state.
Runmoor preserves unresolved records until recovery and completed history for
seven days. Diagnostics retain safe lifecycle information within seven days and
256 MiB; raw workflow output, runner logs and credentials are not copied into them.

Report reproducible problems through [GitHub Issues](https://github.com/delinoio/oss/issues)
with version, platform, stable error code and sanitized status. Community support
has no response SLA, deadline, throughput guarantee or performance SLA.
