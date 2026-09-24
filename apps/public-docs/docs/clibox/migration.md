# Migrating older command syntax

Published version **0.1.6** uses **`env run`** for environment execution. The next release renames it to **`run env`**, rejects `env run` with exit code 2 and migration guidance, and adds `run with-rate-limit`, `run with-lock`, `run with-service`, `run with-retry`, and `run with-timeout`. Keep `env run` and do not add the wrappers when pinned to the published 0.1.6 artifacts; update scripts only when upgrading to a release with the new interface. The environment behavior remains unchanged.

## Changes already available in 0.1.6

The port/hash names and option behavior below are already released in 0.1.6. Update scripts using earlier syntax when upgrading. The previous port/hash command names are rejected with exit code 2 and migration guidance; they are not aliases.

| Previous use | New use |
| --- | --- |
| `clibox port which 3000` | `clibox port list 3000` |
| `clibox hash encode --text hello` | `clibox hash compute --text hello` |
| `clibox port which 3000 --quiet` (PIDs) | `clibox port list 3000 --pids` |
| `--output -` (a file named `-`) | `--output ./-` |

`--quiet` suppresses stdout results on port listing/termination, waits, and hash verification. It never suppresses failure diagnostics or changes the exit status. It conflicts with `--json`; `port list --pids` conflicts with both.

Omitted output and `--output -` both select stdout. `--force` requires an actual file output or `--in-place`; redundant `--in-place --force` is accepted. Remove standalone `--force` and do not combine it with stdout output. Checksum filename rebasing applies only when a manifest is written to an actual file.

Owned operations now return numeric **130** for Ctrl+C/Windows Ctrl+Break and **143** for Unix SIGTERM after cleanup, including text/Base64/hash/time operations that previously returned 1. `run env` (`env run` in version 0.1.6) continues to preserve its child's exit status and Unix termination signal. Completed effects are not undone.

Use `-h` for core rules and examples, or `--help` for those rules plus detailed constraints. Failures remain visible on stderr even with `RUST_LOG=off`.

Check `clibox --version` and command-specific `--help` when working with an older pinned installation. See [Releases and verification](/clibox/releases) for version selection.
