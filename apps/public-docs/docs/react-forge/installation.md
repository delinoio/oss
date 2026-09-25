# Install React Forge

## Requirements

Use Node.js 24 on macOS, Windows, or glibc Linux, with an x64 or arm64 CPU. Alpine/musl, other CPU architectures, and other Node.js major versions are unsupported. Linux needs Fontconfig for system font discovery. Install suitable CJK, RTL, and emoji fonts for the text you generate, or [register fonts explicitly](/react-forge/sessions#fonts-and-assets).

Install the public package with optional dependencies enabled. npm or pnpm selects the matching native package; installation does not compile Rust or download a binary from a separate service.

```sh
npm install @delino/react-forge
```

React Forge currently depends on React 19.2.8 and its matching reconciler. A project using React components should keep its React version compatible with the installed package. Check the installed version with `npm list @delino/react-forge` and see [releases](/react-forge/releases) before pinning an exact version. The first functional public release is `0.1.1`.

## Verify the installation

```sh
npx react-forge --version
npx react-forge --help
```

For a project-local command, use `npx react-forge run hello.tsx --output hello.pptx` after creating the task in [Getting started](/react-forge/getting-started). Keep optional dependencies enabled in deployment and CI installs; a missing matching native package produces an explicit package error. There is no runtime fallback download or build.

Local PPTX, DOCX, XLSX, and PDF work needs no Figma account. [Figma workflows](/react-forge/figma) require a separately connected official Figma MCP server and currently have a narrower live-authentication environment than the six local-document hosts.
