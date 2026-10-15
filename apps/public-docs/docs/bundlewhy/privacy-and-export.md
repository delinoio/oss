# Privacy and export

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development.
:::

## Where results are stored

Results stay in the output directory that you choose with `--out-dir` until you delete them. bundlewhy never uploads results, and it never deletes them for you. There is no telemetry, account or hosted service.

A result bundle contains the analysis, a safe configuration summary and source display paths. Where the build's source map includes original text, the bundle also stores its SHA-256 hash and, unless excluded, its content. If no build-time text snapshot exists, a later live read is not stored or hashed as the analyzed revision. Saved and exported reports mark that source revision as unavailable. With `--exclude-sources`, verified hashes and paths remain, but source content is omitted.

## Exported reports

`bundlewhy export` writes one self-contained HTML file:

- It works offline and needs no server.
- It loads no external resources and makes no network requests.
- The command states whether source content is included.

A report that includes source content can contain secrets that appear in your code, such as keys or internal URLs. bundlewhy does not claim that such a report is secret-free. Review a report before you share it, or export with `--exclude-sources`.

## Local server

- The server binds to `127.0.0.1` only, on a port chosen by the operating system. Other devices on your network cannot connect to it.
- Each run uses a new access token, which is part of the printed URL.
- Requests from other origins are rejected.

Do not share the printed URL while the server runs.

## Logs

Logs never include source content, environment variable values, credentials or access tokens. Each run directory contains a `bundlewhy.log` file with JSON lines for troubleshooting.

## Trust model

Your project configuration and plugins run with your permissions during the diagnostic build, exactly as in your own build. bundlewhy does not sandbox plugin file-system or network access. Analyze only projects that you trust.

## Opening saved results

Saved result bundles and exported reports are treated as untrusted input. bundlewhy validates their format and structure before it shows them, and the UI renders all report content as text. An invalid file fails with `BW_RESULT_INVALID`; an unknown format version fails with `BW_SCHEMA_UNSUPPORTED`.

## Related guides

- [Getting started](/bundlewhy/getting-started#export-an-html-report)
- [Troubleshooting](/bundlewhy/troubleshooting)
