# Ordinary execution environment

- Follow `docs/cmds-delidev-harness-contract.md#ordinary-execution-github-cli-context`. This package owns only an in-memory selector resolved from the original Worker environment and cwd. It must never inspect or own user files, credentials or OS credential stores. Keep its zero value isolated, its path unexported and nonserializable, and diagnostics limited to safe selector classifications.
