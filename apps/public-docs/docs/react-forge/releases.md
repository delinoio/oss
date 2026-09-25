# Releases and validation

`@delino/react-forge` is public on npm. Version `0.1.1` is its first functional release; `0.0.1` only reserved package names, and `0.1.0` was not published. Check the [npm package](https://www.npmjs.com/package/@delino/react-forge) for the current dist-tag before installing or pinning a version. The main package uses matching native optional packages for macOS, Windows, and glibc Linux on x64 and arm64. Keep optional dependencies enabled and versions matched; there is no runtime binary download or compile step.

```sh
npm install @delino/react-forge
npm list @delino/react-forge
```

## Validation scope

The first functional release passed installed-consumer checks across the six native hosts, including local format exports, imports, CLI, and MCP usage. Automated tests cover React session behavior, preserved Office edits, limits, cancellation, fonts, and PDF semantics. Test renderers are verification tools, not runtime dependencies. There is no numerical latency, memory, or throughput guarantee.

Office interoperability evidence uses structural checks and LibreOffice/Poppler rendering on tested hosts. It is **not** direct Microsoft Office validation. Tagged PDF output has semantic structure checks but **not** PDF/UA certification. Figma creation and preservation-aware editing have macOS arm64 live acceptance plus simulated transport coverage; other local-document hosts do not imply live Figma authentication support there.

## Update or roll back

Pin a verified npm version in your project and install its matching optional native package. To roll back, select an earlier **functional** published version, reinstall, and verify `npm list @delino/react-forge` before running tasks. Do not select the name-reservation `0.0.1` or unpublished `0.1.0`. Existing exported documents remain separate files; React Forge does not migrate or replay an in-memory session after a package change.

See [Installation](/react-forge/installation) for supported hosts and [limits and troubleshooting](/react-forge/limits-and-troubleshooting) for recoverable errors. Report reproducible problems through [Delino OSS issues](https://github.com/delinoio/oss/issues).
