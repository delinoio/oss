# Commands

The following interface is planned for the first published version. **There is no released binary to run yet.** Run pnport from an already installed Yarn 4 PnP project:

```text
pnport run -- <command> [args...]
pnport doctor [--json]
pnport cache path
pnport cache list
pnport cache prune
pnport cache clean
pnport --help
pnport --version
```

`run` starts one command with literal arguments and inherited working directory, environment, and standard streams. Bare commands resolve from the selected workspace's direct dependency binaries before the inherited `PATH`. pnport diagnostics use stderr; child stdout remains available for protocols. The child status is propagated. pnport's own argument errors use status 2, missing commands 127, non-executable commands 126, and initialization, runtime, or required-restart errors 125, with stable diagnostic codes distinguishing them from child exits.

`--project` selects a PnP project explicitly. Without it, pnport searches upward from the current directory for the nearest `.pnp.cjs`. `--cache-dir` selects a private cache location, `--log-level=debug` enables detailed diagnostics, and `--color=never` or `NO_COLOR` disables human-output color. These are flags; pnport has no separate configuration file.

`doctor --json` returns one ANSI-free schema-version-1 object with typed checks and stable codes. Cache commands work without an active project. pnport never installs dependencies, repairs an incomplete Yarn install, or changes the lockfile.
