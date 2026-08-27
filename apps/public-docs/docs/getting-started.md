# Getting Started

This page describes how to run and update the Rspress-based documentation app in this repository.

## Prerequisites

- Node.js 24 (`.nvmrc` is the workspace baseline)
- `pnpm` 10+

## Install Dependencies

Run from the repository root:

```bash
pnpm install
```

## Start Local Docs Preview

Run from the repository root:

```bash
pnpm --filter public-docs dev
```

Rspress starts a local docs server for previewing navigation and page content.
The development server port is fixed to `46302`. If it is occupied, the command exits with recovery guidance instead of selecting another port.

## Validate Links

Run from the repository root:

```bash
pnpm --filter public-docs test
```

This builds the site and validates every stable clean route plus generated
internal links. It should pass before opening a pull request.

## Build and Preview

Run from the repository root:

```bash
pnpm --filter public-docs build
pnpm --filter public-docs preview
```

The production build is written to the app's deployment output directory for Cloudflare
Pages. Rspress clean URLs keep public links such as `/getting-started` free of
`.html` suffixes.

## Editing Workflow

1. Update or add page content in the public-docs source.
2. Keep stable routes and navigation entries synchronized.
3. When structural or contract-level behavior changes, update the corresponding internal contracts and policy files as needed.
