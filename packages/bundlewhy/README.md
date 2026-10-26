# bundlewhy

bundlewhy explains why your bundler or its default JavaScript minifier could not apply an optimization to a client production build. It runs one diagnostic build of a trusted local Vite or Rspack project and opens a local web UI. Each diagnostic shows the optimization rule, the condition outcomes, the relevant code, the supporting evidence, the dependency path and a corrective direction with safety conditions. bundlewhy never changes your source code, configuration or lockfiles.

> The commands below require a published npm release. See the [installation guide](https://oss.delino.io/bundlewhy/installation) for current availability.

## Usage

```sh
npx @delino/bundlewhy analyze --out-dir ./bundlewhy-runs
npx @delino/bundlewhy open ./bundlewhy-runs/<run-id>
npx @delino/bundlewhy export ./bundlewhy-runs/<run-id> --output report.html
npx @delino/bundlewhy engines
```

`analyze` options: `--project <dir>`, `--config <file>`, `--config-name <name>` (Rspack multi-build configurations), `--bundler vite|rspack`, `--allow-unsupported-engine`, `--no-open`, `--no-serve` and `--exclude-sources`. Press Ctrl+C to cancel a running analysis; the result is saved and marked incomplete.

## Supported engines

| Bundler                 | Version                 | Default minifier           |
| ----------------------- | ----------------------- | -------------------------- |
| Vite                    | 8.3.4 (Rolldown 1.2.13) | Oxc minifier               |
| Rspack (`@rspack/core`) | 2.2.9                   | SwcJsMinimizerRspackPlugin |

Other versions require `--allow-unsupported-engine`. The results then show that an unsupported engine was selected explicitly.

## Evidence

- **Confirmed**: observed in the output or configuration of the engine used for this analysis.
- **Inferred**: derived by bundlewhy from supporting analysis; not an observed engine decision.
- **Unavailable**: the evidence could not be collected.

Categories without coverage are labeled as such. No findings in a category without coverage is not a clean result.

## Privacy and trust

Results stay in the directory you choose until you delete them. Nothing is uploaded and there is no telemetry. Result bundles and exported reports include source content unless you pass `--exclude-sources`, so review them before sharing. The local server listens on 127.0.0.1 only and requires a per-run access token. Your project configuration and plugins run with your permissions, so analyze trusted projects only.

## Platforms

Node.js 22.18 or newer on macOS arm64, Linux x64 (glibc 2.34 or newer) or Windows x64.

## Support

Report problems at https://github.com/delinoio/oss/issues. There is no support SLA.

## License

Apache-2.0
