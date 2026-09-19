# Troubleshooting

Start with `runlens doctor --json`, the typed error category, and the report's coverage flags. `--log-level debug` enables additional Runlens diagnostics; `--log-level off` suppresses structured logs. Neither option captures child output into reports.

| Result | Action |
| --- | --- |
| Unsupported before launch | Check native architecture, protected executable/interpreter restrictions, and tracing prerequisites. The requested command was not started. |
| Incomplete collection | Inspect limits, unsupported children, and snapshot unknown states. Increase bounded limits explicitly if appropriate; do not treat missing records as no access. |
| Input/configuration error | Check schema version, argv arrays, forward-slash patterns, relative working directory, and environment names. |
| Child failure | Diagnose the original child's terminal output. Its status remains separate from the Runlens result. |
| Preparation failure | Declare the required setup and selected environment names explicitly. Runlens will not copy credentials or infer installation. |
| Timeout/cancellation | The owned process tree is terminated with a five-second grace. Inspect retained incomplete evidence if a report was saved. |
| Save failure | Use an existing writable parent directory and a new destination. Existing reports are never overwritten. |
| Cleanup failure | Inspect only Runlens-owned temporary material named by the recovery classification after confirming its processes have stopped. Do not remove unrelated temporary files. |

The default memory threshold is 256 MiB; collection spills to private temporary files. Default collection limits are 1 GiB and one million paths, configurable through `[limits]`. These bounds apply to collection, not to disk/memory used by the build or its temporary checkout. Exhaustion remains incomplete and cannot pass a check.

For support, file a [GitHub issue](https://github.com/delinoio/oss/issues) with version, OS/architecture, doctor classifications, minimal reproduction, and a reviewed metadata report if useful. Do not attach credentials, source bodies, or captured logs. No response SLA or fixed release deadline is promised.
