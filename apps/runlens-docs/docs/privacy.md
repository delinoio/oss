# Privacy and metadata

Reports contain metadata only. Runlens never stores file bodies, stdin, stdout, stderr, terminal transcripts, or environment variable values, even as an optional capture feature. File contents are streamed through SHA256 and discarded after hashing.

Ordinary runs retain no report, history database, search index, or implicit last-run state. Temporary collection data is private and removed after completion, failure, or handled cancellation. Saved reports are user-owned files without automatic retention policies.

Arguments and paths are masked before report serialization or Runlens logging. Workspace, home, and execution-temporary roots are normalized. Common credential flags and token shapes are masked, and values of designated secret environment variables are removed. Configure additional patterns, environment names, and argument indices under `[redaction]`.

Automatic masking is **not infallible secret detection**. Names, arguments, file sizes, hashes, source revisions, access patterns, and timestamps can still be sensitive. Review reports before sharing. Redacted or ambiguous paths limit comparisons and cannot establish a verification pass.

Runlens uses local structured diagnostics with execution IDs, stages, counts, elapsed time, and stable classifications. Child streams remain the child's output; avoid publishing your terminal transcript. Runlens has no telemetry, remote logging, hosted accounts, cloud history, dashboard, metrics service, remote flags, or alerting backend.
