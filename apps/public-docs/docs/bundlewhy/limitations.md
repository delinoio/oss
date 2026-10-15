# Limitations

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development.
:::

## Analysis scope

- bundlewhy does not explain every internal optimizer pass. It reports only the [rules](/bundlewhy/interpreting-diagnostics#rules) that have verified detectors. Inlining and constant folding currently have no coverage.
- bundlewhy does not change your code or configuration and does not apply fixes automatically.
- It does not run before-and-after experiments and never estimates savings.
- bundlewhy derives export usage paths for Rspack from edges that the internal `RsdoctorPlugin` API reports, and that API can change between releases. When it is unavailable, or Rspack reports no edge for a module, the diagnostic shows export usage as unavailable. The edges are export dependencies that Rspack reported, and the paths that bundlewhy derives from them are not function calls, runtime execution, removable code or savings, and at most five chains of 24 edges are shown per diagnostic.
- Only client production JavaScript builds are analyzed. SSR, library, Node-target and development-server builds are not supported, and there are no CSS diagnostics.
- Only the engine versions on the [supported engines](/bundlewhy/supported-engines) page are supported.

## Runs and results

- One run analyzes one build of one project. There is no batch analysis of a whole monorepo.
- There is no watch mode, automatic retry, resume, caching or comparison between runs.
- Saved results are not migrated between format versions. A bundlewhy version opens only the result format it supports.

## Interface

- The UI and messages are in English only.
- The UI is designed for desktop browsers. At widths of 1280px or more it shows three columns: categories, the diagnostic list and the details. In narrower windows it switches between the list and the details.

## Performance

There is no fixed performance target. Analysis time and memory use depend on the size of your build.
