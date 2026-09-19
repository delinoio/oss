# Understand what a command touched

Runlens observes finite, noninteractive commands and explains filesystem accesses, changes, execution differences, and verification results. It supports developers and CI maintainers investigating hidden dependencies or unstable outputs.

**Release status:** version 0.1.0 is being validated. A build or a documentation page does not establish a published release or platform certification. Installation becomes available when the [release requirements](/releases) are satisfied.

Runlens provides nine capabilities:

1. [Compare executions](/commands): examine two explicitly saved reports.
2. [Audit cache declarations](/commands): identify observed inputs and outputs outside declarations.
3. [Produce receipts](/reports): distinguish access attempts from actual file changes.
4. [Verify clean execution](/verification): execute in a temporary checkout.
5. [Check policies and baselines](/configuration): fail CI on explicit violations.
6. [Explain a file](/commands): find commands that accessed or changed it.
7. [Repeat verification](/verification): compare declared outputs from fresh environments.
8. [Identify potential conflicts](/commands): inspect overlapping writes and read/write relationships.
9. [Export reports](/reports): share metadata as JSON or offline HTML.

```sh
runlens run --save build.json -- your-build-tool build
runlens receipt build.json
```

Without `--save`, Runlens retains no report, history database, search index, or last-run pointer. Child output remains on the terminal; it is not copied into reports. Review [privacy](/privacy) and [platform prerequisites](/platforms) before running commands.

Runlens does not schedule tasks, reuse caches, manage services, or provide a security sandbox. Missing evidence is unknown, not proof that nothing changed.
