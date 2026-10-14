# bundlewhy

bundlewhy explains why Vite or Rspack, and their default JavaScript minifier, could not apply an optimization to a client production build. You run a local command-line tool that performs one diagnostic build of your project, then explore the results in a local web UI in your browser.

:::warning Availability
bundlewhy is not yet published to npm. Version 0.1.0 is in development, and the commands on these pages describe the prepared 0.1.0 behavior. See [installation](/bundlewhy/installation) for the planned entry point and platforms.
:::

## What each diagnostic shows

Each diagnostic connects:

- The **optimization rule** that applies, with its category (for example tree shaking or chunk splitting).
- The **conditions** the rule checks, each with a Met, Unmet or Unknown outcome.
- The relevant **code location** in your source, or in the generated chunk when no source map is available.
- The **evidence** that supports the finding, labeled Confirmed, Inferred or Unavailable.
- The **dependency paths** that bring the module into the build.
- A **corrective direction**, with the conditions under which it is safe to apply.

bundlewhy explains; it never changes your code or configuration. You decide what to change.

## Where it runs

- A local CLI starts one diagnostic build with your project's own installed bundler.
- A local web UI, served only on `127.0.0.1`, shows the results. You can also export a self-contained HTML report that works offline.
- Results stay in a directory that you choose. Nothing is uploaded, and there is no telemetry or account.

Only client production JavaScript builds of trusted local projects are supported. The [supported engines](/bundlewhy/supported-engines) page lists the exact Vite and Rspack versions.

## Guides

- [Installation](/bundlewhy/installation)
- [Getting started](/bundlewhy/getting-started)
- [Supported engines](/bundlewhy/supported-engines)
- [Interpreting diagnostics](/bundlewhy/interpreting-diagnostics)
- [Privacy and export](/bundlewhy/privacy-and-export)
- [Limitations](/bundlewhy/limitations)
- [Troubleshooting](/bundlewhy/troubleshooting)

Report problems through [Delino OSS GitHub Issues](https://github.com/delinoio/oss/issues). There is no support response-time commitment.
