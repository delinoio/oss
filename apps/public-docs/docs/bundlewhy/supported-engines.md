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

## Name mangling setting

For Vite, bundlewhy records whether the Oxc minifier mangles names. It reads the setting from the final output options of the analyzed build, after every plugin `outputOptions` hook ran, and shows it in the configuration summary as `build.rolldownOptions.output.minify.mangle` (`enabled` or `disabled`). Name mangling is disabled when the final `minify` option is `false`, `"dce-only"` or has no `mangle` step (for example `mangle: false` in your configuration, or a plugin that removes it), and when `build.minify` is `false`. With `build.minify: 'terser'` or `'esbuild'`, the Rolldown output option does not apply and the setting is `enabled`. When mangling is disabled, bundlewhy reports no `minifier/direct-eval-blocks-mangling` finding and marks name mangling not applicable. Rspack builds do not report this setting, so bundlewhy reads it as `enabled`.

The Vite collector (adapter version `2`) writes this setting. Reports from earlier collector versions do not contain it and are read as `enabled`.

## The diagnostic build

bundlewhy never substitutes another engine. The diagnostic build always uses your project's installed engine, with collection overrides:

- The build writes to a separate output directory inside the bundlewhy run directory, not to your normal output directory.
- Source maps are enabled.
- For Vite, a collector plugin records the build information and keeps the configured relative or absolute root for the diagnostic build. Without a configured root, Vite uses the selected project directory.
- For Rspack, production mode is forced and an observation-only `RsdoctorPlugin` is appended to your plugins to report export usage. It does not change the build output, and bundlewhy skips it (and shows export usage as unavailable) when your Rspack has no such plugin or your configuration already uses one.

Findings describe that diagnostic build. Its output is not guaranteed to be equivalent to your normal build. The UI shows the exact engine, adapter and override details for each run.

## Supported builds

Only client production JavaScript builds are supported. Server-side rendering (SSR), library, Node-target and development-server builds are not analyzed; selecting one fails with `BW_UNSUPPORTED_BUILD`.

## Related guides

- [Interpreting diagnostics](/bundlewhy/interpreting-diagnostics)
- [Limitations](/bundlewhy/limitations)
