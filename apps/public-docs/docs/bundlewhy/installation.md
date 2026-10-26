# Install bundlewhy

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development. The npm entry point below is planned and does not work until a release is published.
:::

## Requirements

- Node.js 22.18 or newer. bundlewhy runs your project's build through Node.js.
- A local Vite or Rspack project at a version listed on the [supported engines](/bundlewhy/supported-engines) page, with its dependencies installed.
- A desktop browser for the local web UI.

## Planned platforms

| Platform      | Architecture |
| ------------- | ------------ |
| macOS         | arm64        |
| Linux (glibc) | x64          |
| Windows       | x64          |

Linux x64 requires glibc 2.34 or newer.

Other platforms are not planned for 0.1.0. On an unsupported platform, or when the platform package is missing, the launcher reports an error with reinstall guidance. It does not download or compile a fallback.

## Planned npm entry point

When bundlewhy is published, you will run it through npm without a global install:

```sh
npx @delino/bundlewhy --version
npx @delino/bundlewhy analyze --out-dir ./bundlewhy-runs
```

This package is not yet published. Until it is, the command fails because npm cannot find the package.

## Build from source

Building bundlewhy from the repository source is intended for contributors who work on bundlewhy itself. It is not a supported installation path for application developers.

## Update or roll back

bundlewhy has no self-update. To update, run a newer version. To roll back, install or run a previous version. Each version reads only the result format it writes; see [troubleshooting](/bundlewhy/troubleshooting#result-and-report-problems) if an older or newer bundlewhy cannot open a saved run.

## Next steps

- [Getting started](/bundlewhy/getting-started)
- [Supported engines](/bundlewhy/supported-engines)
