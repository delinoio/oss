# Output and cancellation

## Input, output, and exit codes

File and string selectors are mutually exclusive. With neither, clibox reads stdin until EOF; `--input -` explicitly selects stdin. Explicit file/string input does not consume stdin. `--text` supplies its exact UTF-8 bytes without an implicit newline. Base64 and hashes accept arbitrary binary file/stdin input. Text replacement requires valid UTF-8.

Results default to stdout. Text and Base64 add no newline; time values, generated hashes, and verification reports end with LF. `--output FILE` writes the result only to that file. `--output -` explicitly selects stdout; use `--output ./-` for a literal dash filename. `--force` requires an actual file output or `--in-place`; standalone force and force with stdout are argument errors.

Exit status is `0` for success, `1` for runtime failure or checksum mismatch, `2` for missing, malformed, or conflicting arguments, `130` for Ctrl+C/Windows Ctrl+Break, and `143` for Unix SIGTERM. Diagnostics use stderr. `RUST_LOG` enables more detailed structured diagnostics; the default is warnings/errors. Color requires a TTY and is disabled by `NO_COLOR`. Diagnostics omit input content, digests, patterns, replacements, raw arguments, and paths. Verification filenames are intentional command results.

Operations are offline and use current OS permissions. No settings, history, telemetry, or automatic retry is added outside the explicit [execution wrappers](/clibox/system#coordinate-execution). Named rate limits and locks retain only private local coordination state. No fixed input/output size limit is imposed: text replacement holds the entire input/result in memory, while Base64 and hashing stream bytes. Resource exhaustion can fail an operation. **Streaming stdout may already contain partial output when reading, decoding, writing, or interruption fails.** Use `--output` when an incomplete result must not replace a file.

If an execution wrapper cannot forward an owned workload or managed-service stream, it stops owned work and returns a runtime failure.

## File replacement

Output is prepared in a temporary file beside its destination and published only after processing succeeds. Existing output requires `--force`. Replacement does not require reading the existing file contents; metadata and security information must remain accessible. `--in-place` still needs read access to its input. Access permissions, including supported native ACLs and ownership, are preserved on replacement; inability to preserve them fails instead of silently discarding them. Symbolic-link and multiply-linked replacement destinations are rejected. On Windows, replacing a read-only file requires attribute-write and replacement permission and preserves its read-only status. Unsupported filesystem replacement capabilities fail without changing the original.

`text replace --in-place` requires one explicitly selected regular input file, conflicts with `--text` and `--output`, and itself authorizes replacement without `--force`. Invalid UTF-8, invalid patterns/references, and required-match failures leave the original intact. Unpublished temporary files are cleaned up after handled failures/interruption. Completed replacements are not undone.

There are no automatic backups, file locks, or concurrent-modification checks. The last successful replacement wins; atomic publication does not prevent lost updates. Pin an earlier clibox package version to roll back command behavior; this does not restore overwritten files.

## Utility diagnostics and operation limits

All operations use the current OS user's permissions and desktop session. No authentication service, saved configuration, operation history, or telemetry is added. Environment, port, open, clipboard, and transformation commands have no automatic retry or fixed execution timeout apart from the shared five-second port-termination verification. The next release's execution wrappers add only their documented local coordination, retry, readiness, and timeout behavior. Readiness waits use the polling and deadline options in [Readiness waits](/clibox/wait). Owned operations return numeric 130 for Ctrl+C/Windows Ctrl+Break and 143 for Unix SIGTERM after cleanup. `run env` (`env run` in version 0.1.6) preserves the delegated child's exit status and Unix signal identity. Interruption does not undo completed copies, terminations, application launches or file replacements.

Configuration input/output and nesting limits are documented in [Configuration commands](/clibox/configuration).

## Execution wrapper statuses

`run env` preserves its delegated child's numeric exit status and Unix signal identity. The next release's `run with-*` wrappers also preserve a natural child status when the wrapper itself succeeds. Their admission, readiness, overall, and idle timeouts return **124**. `run with-lock --on-locked fail` returns **75**; `--on-locked skip` returns **0** without starting a workload. Invalid wrapper arguments return **2** and other wrapper failures return **1**. Handled Ctrl+C/Windows Ctrl+Break returns **130** and Unix SIGTERM returns **143** after owned-work cleanup.

External services observed by `run with-service` are never terminated. A managed service, retry attempt, or timeout-owned workload is stopped on a wrapper timeout, cancellation, or failure. A second cancellation skips any remaining cleanup grace. Wrapper diagnostics remain on stderr and omit names, commands, environment values, URLs, credentials, response data, and paths.

When installed through npm on Unix, clibox forwards SIGINT, SIGTERM, and SIGHUP to the native command, including signals sent directly to the launcher process.

Warnings/errors use structured stderr diagnostics. Set `RUST_LOG=debug` for more detail. Stdout remains dedicated to results or the delegated child's output. Color is used only on a TTY and respects `NO_COLOR`. Clibox-authored diagnostics omit clipboard text, environment values, transformation input, patterns, replacements, digests, complete argv, full URLs and paths; a child program still controls its own inherited output.

For port permission errors, inspect the returned partial results and use the appropriate user/session permissions; clibox does not elevate privileges. Clipboard access needs a reachable desktop session, its normal display authorization and the listed installed tools. A busy Windows clipboard or a Wayland compositor without the required capability produces an actionable failure. Open failures may indicate missing applications, URI associations, Linux xdg-utils or desktop access. Errors after dispatch do not guarantee that nothing opened.

Automated parser, process, mocked OS-adapter and package tests cover these contracts. Real GUI behavior, desktop clipboard persistence and application-wait verification remain follow-up validation; mocked coverage does not establish those desktop observations. To roll back, install an earlier exact package version. Previous OS effects are not undone.

See [Readiness results](/clibox/wait#results-and-cancellation) and [Configuration limits](/clibox/configuration#input-limits-and-output-safety) for their command-specific formats and bounds.
