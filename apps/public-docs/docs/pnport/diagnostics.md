# Diagnostics and troubleshooting

**pnport 0.1.0 is unreleased.** Development diagnostics are useful for source testing, but a complete release and all platform execution gates are still pending.

## Start with doctor

Run `pnport doctor` for human-readable project, platform, injection, and cache checks. `pnport doctor --json` writes one schema-version-1 object to stdout with `ready` and typed `checks`. `ready: false` exits 125; `ready: true` does not guarantee that a specific executable can accept virtualization.

| Symptom | What to check |
| --- | --- |
| Project check fails | Confirm an installed Yarn 4 PnP project and the selected `.pnp.cjs`; pnport does not repair it. |
| Dependency-view conflict | Resolve a physical `node_modules` collision yourself; pnport leaves it untouched. |
| Platform or injection is unsupported | Use a host and executable with a verified release capability; protected executables cannot be forced through pnport. |
| Archive or cache error | Check that the installed package archive is intact and the private cache is writable; inspect [cache management](/pnport/cache). |
| Graph-change restart notice | Stop and start the command again after PnP data or an active archive changes. |

## Exit codes and streams

| Status | Meaning |
| --- | --- |
| `2` | Invalid pnport arguments. |
| `125` | pnport initialization, runtime, required restart, or non-ready doctor result. |
| `126` | Command found but cannot be executed. |
| `127` | Command not found. |

When a child starts successfully, pnport passes through its exit status. Stable `PNPORT_*` codes on stderr distinguish a pnport failure from a child that exits with the same number. Normal missing files remain child-visible filesystem errors rather than always stopping the supervisor.

pnport diagnostics go to stderr; child stdout and stderr are inherited rather than captured. Default logging includes errors and necessary notices. `--log-level debug` can expose paths, so review debug logs before sharing them. pnport diagnostics must not contain file contents, environment values, full argument vectors, or captured child output. There is no pnport telemetry or hosted diagnostic service. Report a sanitized reproduction through [GitHub Issues](https://github.com/delinoio/oss/issues).
