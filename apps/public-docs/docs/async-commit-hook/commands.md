# CLI reference

Machine-facing commands support `--json` with schema_version=1 and diagnostics on stderr. `ach --help` lists command flags.

| Command | Purpose |
| --- | --- |
| init; config validate; version | Trust/register, inspect configuration, show versions. |
| run; status; wait; logs; check | Submit, inspect, wait, read evidence and gate an exact commit. |
| inbox; ack | Recover work and explicitly acknowledge results. |
| failures; plan; doctor; compare; rerun; cancel | Diagnose and control existing work. |
| ui | Start or reuse the local web UI server and print its URL. |
| hooks install/uninstall; pre-push | Preserve existing hooks and optionally enforce branch-tip validation. |
| agent install/uninstall; agent-guide; mcp | Install safe client integration or use generic CLI/MCP. |
| daemon start/status/stop | Manage the account's local daemon; stop drains, --force cancels. |
| prune; self-update | Preview/apply retention or explicitly update a direct installation. |

Exit codes: **0** successful operation (gates only for passing validation); **1** failed/incomplete validation; **2** invalid usage/configuration; **3** runner/storage error; **4** wait expiration. A successful status query may report a failed check.

Useful selectors: `--repo .`, `--commit SHA`, `--run ID`, `--check NAME`. History uses `--limit 50 --cursor TOKEN`; logs use `--offset 0 --limit 65536`. `wait --timeout 60` never terminates checks.
