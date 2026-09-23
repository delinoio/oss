# Editors and long-running tools

**Editor and language-server compatibility has not been certified for a published version.** When a release becomes available, configure an editor's tool command to invoke `pnport run --` inside the already installed Yarn 4 project. Keep the command attached to the selected workspace; a pnport run does not merge independent PnP projects.

Language servers that communicate over standard input and output need clean stdout. pnport forwards the child's streams rather than capturing them, and emits its own diagnostics on stderr. Development servers and watch tools may run indefinitely. A graph or active archive change requires restarting the tool process; ordinary source edits should flow through native watch notifications.

Protected macOS programs, unsupported injection paths, and unavailable Linux syscall capabilities must fail with a diagnostic. Do not bypass an interception error by launching the tool outside pnport and treating that result as PnP compatibility evidence. Report the target platform, `doctor` classification, and reproducible command shape through [GitHub issues](https://github.com/delinoio/oss/issues), without sharing secrets or private file contents.
