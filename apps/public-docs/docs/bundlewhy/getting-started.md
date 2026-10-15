# Getting started with bundlewhy

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development. The examples use the `bundlewhy` command; see [installation](/bundlewhy/installation) for the planned entry point.
:::

## Analyze a project

Run `analyze` in the directory of a trusted Vite or Rspack project:

```sh
bundlewhy analyze --out-dir ./bundlewhy-runs
```

`--out-dir` is required. bundlewhy creates a new run directory inside it, named with a new UUID v7 identifier, and keeps the results there until you delete them.

bundlewhy then:

1. Detects the bundler and its configuration file.
2. Runs one diagnostic production build with your installed bundler.
3. Collects, analyzes and measures the build output, then saves the results.
4. Starts a local server and prints a `127.0.0.1` URL that contains a per-run access token.
5. Opens the URL in your default browser.

If the browser does not open, open the printed URL manually.

## Select the project, configuration and bundler

By default, bundlewhy analyzes the current directory and selects the bundler and configuration automatically when exactly one candidate of each exists. Otherwise, select them explicitly:

| Flag                                   | Purpose                                                               |
| -------------------------------------- | --------------------------------------------------------------------- |
| `--project <dir>`                      | Analyze another project directory.                                    |
| `--config <file>`                      | Use this bundler configuration file.                                  |
| `--config-name <name>`                 | Select one build when an Rspack configuration exports several builds. |
| `--bundler vite` or `--bundler rspack` | Select the bundler when several are candidates.                       |

## Other analyze options

| Flag                         | Effect                                                                                                     |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `--no-open`                  | Start the local server but do not open a browser.                                                          |
| `--no-serve`                 | Save the results and exit without starting the local server.                                               |
| `--exclude-sources`          | Store source paths and available build-snapshot hashes, but not source content.                            |
| `--allow-unsupported-engine` | Analyze with an installed engine version outside the [compatibility matrix](/bundlewhy/supported-engines). |

Global flags are `--verbose`, `--no-color`, `--help` and `--version`.

## Cancel or stop

Press Ctrl+C while an analysis runs to cancel it. bundlewhy stops the build, saves the run as cancelled and marks its results incomplete. Press Ctrl+C after the analysis finishes to stop the local server; the saved results stay in the run directory.

## Open a saved run

```sh
bundlewhy open ./bundlewhy-runs/<run-id>
```

`open` validates the saved results and serves them in the local web UI. Add `--no-open` to skip opening the browser.

For a completed run, the elapsed time includes saving the result bundle and attempting its optional summary. The live view, reopened run and exported report use the same recorded time. Older saved runs keep their original elapsed time.

## Export an HTML report

```sh
bundlewhy export ./bundlewhy-runs/<run-id> --output report.html
```

The report is one self-contained HTML file that works offline. Add `--exclude-sources` to omit source content. bundlewhy does not overwrite an existing file unless you add `--force`. Read [privacy and export](/bundlewhy/privacy-and-export) before you share a report.

## Show supported engines

```sh
bundlewhy engines
```

This prints the frozen engine compatibility matrix for the installed bundlewhy version.

## Exit codes

| Code  | Meaning                                                                   |
| ----- | ------------------------------------------------------------------------- |
| `0`   | Success.                                                                  |
| `1`   | Failure. The error output names a `BW_*` code and a recovery instruction. |
| `2`   | Usage error, including an ambiguous bundler or configuration selection.   |
| `130` | Cancelled.                                                                |

## One run at a time

Each `analyze` command performs one new run. bundlewhy does not retry automatically, resume an interrupted run, watch for changes or cache results. To analyze again, start a new run.

## Next steps

- [Interpreting diagnostics](/bundlewhy/interpreting-diagnostics)
- [Troubleshooting](/bundlewhy/troubleshooting)
