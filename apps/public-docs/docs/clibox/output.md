# Output and cancellation

## Input, output, and exit codes

File and string selectors are mutually exclusive. With neither, clibox reads stdin until EOF; `--input -` explicitly selects stdin. Explicit file/string input does not consume stdin. `--text` supplies its exact UTF-8 bytes without an implicit newline. Base64 and hashes accept arbitrary binary file/stdin input. Text replacement requires valid UTF-8.

Results default to stdout. Text and Base64 add no newline; time values, generated hashes, and verification reports end with LF. `--output FILE` writes the result only to that file. `--output -` explicitly selects stdout; use `--output ./-` for a literal dash filename. `--force` requires an actual file output or `--in-place`; standalone force and force with stdout are argument errors.

Exit status is `0` for success, `1` for runtime failure or checksum mismatch, `2` for missing, malformed, or conflicting arguments, `130` for Ctrl+C/Windows Ctrl+Break, and `143` for Unix SIGTERM. Diagnostics use stderr. `RUST_LOG` enables more detailed structured diagnostics; the default is warnings/errors. Color requires a TTY and is disabled by `NO_COLOR`. Diagnostics omit input content, digests, patterns, replacements, raw arguments, and paths. Verification filenames are intentional command results.

Operations are offline and use current OS permissions. No settings, cache, history, telemetry, automatic retries, or fixed execution timeout is added. No fixed input/output size limit is imposed: text replacement holds the entire input/result in memory, while Base64 and hashing stream bytes. Resource exhaustion can fail an operation. **Streaming stdout may already contain partial output when reading, decoding, writing, or interruption fails.** Use `--output` when an incomplete result must not replace a file.

## File replacement

Output is prepared in a temporary file beside its destination and published only after processing succeeds. Existing output requires `--force`. Replacement does not require reading the existing file contents; metadata and security information must remain accessible. `--in-place` still needs read access to its input. Access permissions, including supported native ACLs and ownership, are preserved on replacement; inability to preserve them fails instead of silently discarding them. Symbolic-link and multiply-linked replacement destinations are rejected. On Windows, replacing a read-only file requires attribute-write and replacement permission and preserves its read-only status. Unsupported filesystem replacement capabilities fail without changing the original.

`text replace --in-place` requires one explicitly selected regular input file, conflicts with `--text` and `--output`, and itself authorizes replacement without `--force`. Invalid UTF-8, invalid patterns/references, and required-match failures leave the original intact. Unpublished temporary files are cleaned up after handled failures/interruption. Completed replacements are not undone.

There are no automatic backups, file locks, or concurrent-modification checks. The last successful replacement wins; atomic publication does not prevent lost updates. Pin an earlier clibox package version to roll back command behavior; this does not restore overwritten files.

## Utility diagnostics and operation limits

All operations use the current OS user's permissions and desktop session. No authentication service, saved configuration, cache, operation history or telemetry is added. Environment, port, open, clipboard, and transformation commands have no automatic retry or fixed execution timeout apart from the shared five-second port-termination verification. Readiness waits use the polling and deadline options in [Readiness waits](/clibox/wait). Owned operations return numeric 130 for Ctrl+C/Windows Ctrl+Break and 143 for Unix SIGTERM after cleanup. `env run` preserves the delegated child's exit status and Unix signal identity. Interruption does not undo completed copies, terminations, application launches or file replacements.

Configuration input/output and nesting limits are documented in [Configuration commands](/clibox/configuration).

Warnings/errors use structured stderr diagnostics. Set `RUST_LOG=debug` for more detail. Stdout remains dedicated to results or the delegated child's output. Color is used only on a TTY and respects `NO_COLOR`. Clibox-authored diagnostics omit clipboard text, environment values, transformation input, patterns, replacements, digests, complete argv, full URLs and paths; a child program still controls its own inherited output.

For port permission errors, inspect the returned partial results and use the appropriate user/session permissions; clibox does not elevate privileges. Clipboard access needs a reachable desktop session, its normal display authorization and the listed installed tools. A busy Windows clipboard or a Wayland compositor without the required capability produces an actionable failure. Open failures may indicate missing applications, URI associations, Linux xdg-utils or desktop access. Errors after dispatch do not guarantee that nothing opened.

Automated parser, process, mocked OS-adapter and package tests cover these contracts. Real GUI behavior, desktop clipboard persistence and application-wait verification remain follow-up validation; mocked coverage does not establish those desktop observations. To roll back, install an earlier exact package version. Previous OS effects are not undone.

See [Readiness results](/clibox/wait#results-and-cancellation) and [Configuration limits](/clibox/configuration#input-limits-and-output-safety) for their command-specific formats and bounds.
