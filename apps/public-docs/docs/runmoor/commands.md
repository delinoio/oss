# Runmoor Commands and Routing

> **Version note:** Runmoor 0.2.0 introduced automatic setup and managed runner updates. Guided Mac VM creation during `init` and `image create --ipsw latest` are in the next release. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor version` before using release-specific commands.


```yaml
jobs:
  build:
    runs-on: runmoor-linux
    steps:
      - run: echo "Running on an ephemeral local runner"
```

Use the configured scale-set label, or a matching label array, and applicable GitHub runner-group policy. Runmoor receives demand without a public webhook endpoint. Ordinary NAT networking is sufficient.
For a new ARM64 Docker pool, `runs-on: [runmoor-linux, linux, ARM64]` also
selects its platform and architecture; use `x64` for an amd64 pool. The
single scale-set label above continues to work.

```sh
runmoor status
runmoor status --json
runmoor doctor --json
runmoor pause --pool linux
runmoor resume --pool linux
runmoor reload
runmoor drain
runmoor stop
runmoor stop --force
```

Status and doctor JSON use `schema_version: 1`. Status includes image revisions, calculated budgets, managed runner versions, update attempts and pending recovery. Doctor also reports the current state and briefly acquires/releases OS sleep inhibition to check permissions. Errors have stable codes, affected identifiers and a recovery action. English text is usable without color; use `--no-color` or `NO_COLOR` to disable ANSI logs. JSON never uses color.

`pause` stops acquisition/new capacity and preserves running jobs. `drain` additionally waits for jobs/local cleanup. `stop` drains before exiting and waits for open image setup and pending image removal, including with `--force`. Finish setup by shutting down its VM or sealing the revision, and retry pending removal as needed. New image work requires restarting the manager after stop; `stop --pool NAME` drains that pool while the manager keeps serving other pools. Only explicit `--force` terminates owned work. `resume` revalidates the pool. Pool control commands without `--pool` apply to all pools. A validated reload automatically resumes a suspended pool only when a setting related to its reported failure changed; a verified managed image can also recover image, version or repeated startup failures. Otherwise, correct the cause and use `resume`.

Reload validates the requested configuration first. Managed image changes prepare before activation; preparation failures retain the previous verified environment. Existing jobs retain their original configuration and timeout. Removed/changed pools drain their previous generation; a new generation with the same GitHub scale-set identity waits until the old one retires. A failed reload leaves the last valid configuration active.

Real demand receives capacity before warm runners. Round-robin allocation shares remaining resources across pools. Minimum idle is best effort; running work is never preempted. CPU/memory reservations include DinD and image setup. Low disk blocks new work without evicting active jobs or sealed images.

## Complete CLI reference

All commands accept `--config PATH` and `--no-color`. Commands and flags are case sensitive.

| Command | Additional options and behavior |
| --- | --- |
| `init` | Interactive Tart setup can create and supervise a new Apple-IPSW VM, then resume after interruption; scripted setup uses `--target`, `--backend`, `--auth`, `--credential-env`/`--credential-file`, App IDs and `--image`/`--image-source`; `--image-only` creates an empty setup configuration; never overwrites |
| `config validate` | Reject unknown fields, versions and contradictory budgets |
| `config show --resolved` | Read calculated settings and committed runner versions without preparing images |
| `runner update` | Optional `--pool NAME`; request an immediate managed runner check |
| `run` | Foreground manager; interruption requests a drain |
| `status`, `doctor` | `--json` for stable versioned structured output |
| `reload` | Validate and atomically accept the whole candidate |
| `pause`, `resume`, `drain` | Optional `--pool NAME`; drain waits for cleanup |
| `stop` | Optional `--pool NAME` and `--force`; whole-manager stop exits |
| `version` | Print version and source revision |
| `service install`, `start`, `stop`, `uninstall` | Operate the user service; use the `service` prefix for each |
| `image create` | `--name NAME`, optional `--cpu N` and `--memory-mib N`, exactly one `--ipsw latest\|PATH` or `--from SOURCE`; optional `--source-home PATH` for an external local image |
| `image open`, `remove` | `--id UUID`; use the `image` prefix for each |
| `image seal` | `--id UUID`, optional `--runner-version latest|VERSION` and `--runner-path PATH`; omission installs latest |
| `image list` | Optional `--json`; includes preparation/sealed revisions and problems |

Image commands print structured revision data. Image changes require a running manager; `image list` also works offline. See [Tart image preparation](./tart) for starting without any pools. CPU values are whole cores and memory values are MiB. Use `--help` to list commands. Read [operations and recovery](./operations) before force-stop or image removal.

## Host selection and control

The unreleased [macOS host backend](./host) is selected explicitly with
`runmoor init --backend host`. New workflows route with
`runs-on: [runmoor-macos-host, macOS, ARM64]`. The existing status, doctor,
runner update, reload, pause, resume, drain, stop, scoped force-stop and launchd
service commands apply to host pools. Image commands remain Tart-only.

Host work shares global admission budgets, but CPU/memory reservations do not
enforce process usage limits. Force-stop retains uncertain ownership and cleanup;
it never signals unrelated account processes or deletes replaced directories.
Actual host execution, unsigned Xcode builds and live GitHub jobs are unvalidated.
