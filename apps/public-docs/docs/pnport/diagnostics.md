# Diagnostics and troubleshooting

**pnport is unreleased; installation and runtime checks are not yet available.**

After a release is available, run `pnport doctor` in the installed Yarn 4 project to check project data, platform capabilities, injection prerequisites, and cache access. `pnport doctor --json` provides schema-version-1 typed checks for automation; it does not include ANSI progress text. Use `--log-level=debug` only when additional local detail is needed.

A missing or mismatched npm native package requires reinstalling the same exact launcher and platform package version with optional dependencies enabled. A standalone archive must contain the matching interception library next to the executable. pnport does not download, compile, or silently select an unrelated binary when either file is missing.

Common failure classes include missing or malformed PnP data, a physical `node_modules` conflict, unsupported interception, archive corruption, cache access failure, graph change, and cleanup failure. A normal missing file inside a child remains a child-visible filesystem result. A graph-change diagnostic requires restarting the command after Yarn's installation is complete.

Debug output may contain paths. pnport diagnostics must not collect file contents, environment values, full argument lists, or captured child output. Remove any private paths from issue reports before posting them. [GitHub issues](https://github.com/delinoio/oss/issues) are the support channel.
