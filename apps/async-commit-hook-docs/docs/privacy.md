# Privacy and trust

Results, logs, reports and acknowledgements stay on your computer. Static hosting does not receive them. No telemetry, analytics or automatic diagnostic uploads are sent. Operational logs record typed lifecycle events without credentials or complete environments.

Registration trusts your repository's commands, including later committed changes. Commands run on the host; source isolation is not a sandbox for hostile code. ach supplies only declared environment inputs and necessary tool context. Known secret values are redacted before logs/reports are stored, including values spanning output chunks; redaction cannot discover every undeclared secret in arbitrary output.

Submodule and Git LFS source are explicitly unsupported. Each committed `.gitattributes` file must be at most 1 MiB; larger files are rejected before an execution is accepted. A single Git tree record (path plus object metadata) must also fit within 1 MiB; the total number of paths has no product limit. Docker is not required. Managed checks do not recursively trigger automatic checks. Original worktrees and unrelated processes are preserved.
