# Getting started

**pnport 0.1.0 is unreleased; the commands below describe the CLI interface, not an available installation.** Prepare an installed Yarn 4 Plug'n'Play project first. The project must have `.pnp.cjs`; inline and split PnP data are part of the release contract.

## Check the project

From the directory where a future subprocess should run, `pnport doctor` checks the selected PnP project, platform, injection artifact, and cache. `pnport doctor --json` provides the same checks as one machine-readable object. A non-ready report exits with status 125. Doctor readiness does not certify a particular child executable; `run` checks its own admission requirements.

Without `--project`, pnport searches upward from the current directory for the nearest `.pnp.cjs`. Use `--project` to choose another project directory or its `.pnp.cjs` file. Project selection does not change the child's working directory.

## Run a command

After a complete release is available, the invocation shape is:

```text
pnport run -- <command> [args...]
pnport --project <installed-project> run -- <command> [args...]
```

The `--` separates pnport options from the command and its literal arguments. Bare command names are resolved from the selected workspace's direct dependency binaries before the inherited `PATH`; explicit executable paths remain explicit. pnport does not wrap the command in a shell. If a dependency binary name is ambiguous, choose an explicit path.

The child inherits the working directory, environment, and standard streams. pnport diagnostics go to stderr; the child's stdout remains available for a language server or other protocol. The child's exit status is preserved when it starts successfully. See [filesystem and processes](/pnport/filesystem-and-processes) and [diagnostics](/pnport/diagnostics) before using a development server or watch process.

If the project has a physical `node_modules` directory that conflicts with the virtual dependency view, pnport reports a conflict instead of deleting or changing it. Resolve that conflict deliberately in the project before retrying.
