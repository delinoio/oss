# Supported engines

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development. This matrix is frozen for 0.1.0.
:::

## Compatibility matrix

| Bundler | Package        | Version                     | Default JavaScript minifier          |
| ------- | -------------- | --------------------------- | ------------------------------------ |
| Vite    | `vite`         | `8.3.4` (Rolldown `1.2.13`) | Oxc minifier (`build.minify: 'oxc'`) |
| Rspack  | `@rspack/core` | `2.2.9`                     | `SwcJsMinimizerRspackPlugin`         |

Run `bundlewhy engines` to print the matrix for the installed bundlewhy version.

## Other engine versions

If the installed bundler version is not in the matrix, analysis fails with `BW_ENGINE_VERSION_UNSUPPORTED` before the build starts.

To analyze anyway, pass `--allow-unsupported-engine`. bundlewhy then uses the installed version, and the results are marked "unsupported engine selected" in the UI and in exported reports. Findings for an unsupported version can be incomplete or wrong.

## Non-default minifiers

If the build uses a JavaScript minifier other than the default in the matrix, bundlewhy still analyzes module-graph categories such as tree shaking and chunk splitting. Minifier categories are marked unavailable and the run reports `BW_MINIFIER_NON_DEFAULT`.

If minification is disabled, bundlewhy reports a confirmed configuration diagnostic and `BW_MINIFICATION_DISABLED`.

## The diagnostic build

bundlewhy never substitutes another engine. The diagnostic build always uses your project's installed engine, with collection overrides:

- The build writes to a separate output directory inside the bundlewhy run directory, not to your normal output directory.
- Source maps are enabled.
- For Vite, a collector plugin records the build information and keeps the configured relative or absolute root for the diagnostic build. Without a configured root, Vite uses the selected project directory.
- For Vite, the collector also records whether the build mangles names, read from the output options after every plugin `outputOptions` hook ran. Name mangling is reported as not applicable when the build turns mangling off (`output.minify: false`, `'dce-only'` or `{ mangle: false }`).
- For Vite, the collector also records which chunks each emitted chunk loads statically and dynamically, read from the bundle that Vite returns after every plugin hook ran. If an output plugin changed which modules a chunk holds, or edited a chunk's generated code, after the collector took its snapshot, the collector discards this chunk information, and bundlewhy does not report chunk-splitting findings that depend on it as proven.
- For Rspack, production mode is forced and an observation-only `RsdoctorPlugin` is appended to your plugins to report export usage. It does not change the build output, and bundlewhy skips it (and shows export usage as unavailable) when your Rspack has no such plugin or your configuration already uses one.

Findings describe that diagnostic build. Its output is not guaranteed to be equivalent to your normal build. The UI shows the exact engine, adapter and override details for each run.

## Supported builds

Only client production JavaScript builds are supported. Server-side rendering (SSR), library, Node-target and development-server builds are not analyzed; selecting one fails with `BW_UNSUPPORTED_BUILD`.

## Related guides

- [Interpreting diagnostics](/bundlewhy/interpreting-diagnostics)
- [Limitations](/bundlewhy/limitations)
