# Runmoor Commands and Routing

```yaml
jobs:
  build:
    runs-on: runmoor-linux
    steps:
      - run: echo "Running on an ephemeral local runner"
```

Use the configured scale-set label, or a matching label array, and applicable GitHub runner-group policy. Runmoor receives demand without a public webhook endpoint. Ordinary NAT networking is sufficient.

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

Status and doctor JSON use `schema_version: 1`. Status includes image revisions and pending recovery. Doctor also reports the current state and briefly acquires/releases OS sleep inhibition to check permissions. Errors have stable codes, affected identifiers and a recovery action. English text is usable without color; use `--no-color` or `NO_COLOR` to disable ANSI logs. JSON never uses color.

`pause` stops acquisition/new capacity and preserves running jobs. `drain` additionally waits for jobs/local cleanup. `stop` drains before exiting and waits for open image setup and pending image removal, including with `--force`. Finish setup by shutting down its VM or sealing the revision, and retry pending removal as needed. New image work requires restarting the manager after stop; `stop --pool NAME` drains that pool while the manager keeps serving other pools. Only explicit `--force` terminates owned work. `resume` revalidates the pool. Pool control commands without `--pool` apply to all pools. Resume a suspended pool after correcting credentials or preparation failures.

Reload validates the entire candidate first. Existing jobs retain their original configuration and timeout. Removed/changed pools drain their previous generation; a new generation with the same GitHub scale-set identity waits until the old one retires. A failed reload leaves the last valid configuration active.

Real demand receives capacity before warm runners. Round-robin allocation shares remaining resources across pools. Minimum idle is best effort; running work is never preempted. CPU/memory reservations include DinD and image setup. Low disk blocks new work without evicting active jobs or sealed images.

## Complete CLI reference

All commands accept `--config PATH` and `--no-color`. Commands and flags are case sensitive.

| Command | Additional options and behavior |
| --- | --- |
| `init` | Create an incomplete annotated configuration without overwriting |
| `config validate` | Reject unknown fields, versions and contradictory budgets |
| `run` | Foreground manager; interruption requests a drain |
| `status`, `doctor` | `--json` for stable versioned structured output |
| `reload` | Validate and atomically accept the whole candidate |
| `pause`, `resume`, `drain` | Optional `--pool NAME`; drain waits for cleanup |
| `stop` | Optional `--pool NAME` and `--force`; whole-manager stop exits |
| `version` | Print version and source revision |
| `service install`, `start`, `stop`, `uninstall` | Operate the user service; use the `service` prefix for each |
| `image create` | `--name NAME`, `--cpu N`, `--memory-mib N`, exactly one `--ipsw PATH` or `--from SOURCE`; optional `--source-home PATH` for an external local image |
| `image open`, `remove` | `--id UUID`; use the `image` prefix for each |
| `image seal` | `--id UUID`, `--runner-version VERSION`, optional `--runner-path PATH` |
| `image list` | Optional `--json`; includes preparation/sealed revisions and problems |

Image commands print structured revision data. Image changes require a running manager; `image list` also works offline. See [Tart image preparation](/runmoor/tart) for starting without any pools. CPU values are whole cores and memory values are MiB. Use `--help` to list commands. Read [operations and recovery](/runmoor/operations) before force-stop or image removal.
