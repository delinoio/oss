# React Forge public documentation

## Scope

`apps/public-docs/docs/react-forge` owns the English user guides at `https://oss.delino.io/react-forge/`. The existing `public-docs` Rspress app builds and publishes this section with the other major projects. There is no separate documentation workspace, hosting target, or development port.

## Audience and source of truth

Node.js 24 developers using the public `@delino/react-forge` library or CLI and trusted local MCP clients are the primary audience. Curate from `docs/project-react-forge.md`, the package, Figma, static-scene, MCP, release, and validation contracts, the public types, and the npm README. Preserve source-backed behavior and limitations. Keep architecture, repository paths, release operations, and evidence reproduction commands in `docs/`; public pages describe supported user workflows, stable interfaces, availability, and honest validation limits.

## Routes and content

The stable clean routes are `/react-forge/`, `/installation`, `/getting-started`, `/sessions`, `/pptx`, `/docx`, `/xlsx`, `/pdf`, `/glb`, `/fbx`, `/office-editing`, `/figma`, `/cli`, `/mcp`, `/limits-and-troubleshooting`, and `/releases`, all under `/react-forge`.

- Installation and getting started cover Node.js 24, six local-document host combinations, optional native packages, fonts, and an executable TSX task.
- Sessions and format guides cover the public React/session APIs, all four local document models, explicit exports, Office preservation, font/layout limits, and the absence of PDF import or Office conversion.
- GLB and FBX guides describe the unreleased generation-only SceneSession API, registered binary assets, world-space bounds, Blender 4.5 material profile, resource limits, and exclusions. They explicitly state that npm 0.1.1 does not include this extension; hosted-platform checks are not implied by local evidence.
- Figma explains its narrower macOS Keychain authentication boundary, official MCP connection, explicit remote publication, complete/partial/unknown receipts, and recovery without blind retries. Receipts may omit a file locator after ambiguous creation; guide readers to establish the remote file's identity before reopening or repeating any write.
- CLI and MCP guides distinguish one-shot tasks from retained in-memory sessions and explain cancellation, trust, output isolation, and explicit publication.
- Limits and releases document typed recovery, resource ceilings, first functional npm version `0.1.1`, six-host local-document validation, and the absence of direct Microsoft Office validation or PDF/UA certification. Current install guidance should resolve npm's published dist-tag instead of claiming an old version is perpetually latest.

Every route appears in the Rspress sidebar and the shared selector identifies `/react-forge` as React Forge. The root landing page and project catalog link to the canonical same-origin section; the npm README points there. No duplicate root guide or handoff route is added.

## Build and validation

Use the consolidated `pnpm dev:public-docs` loopback server on port `46302` and the existing Cloudflare Pages publication. Run `pnpm test` from `apps/public-docs`. Rendered validation checks all 16 artifacts, headings and guide links, sidebar entries, exact selector state, repository links, clean routes, and prohibited credential/internal-path content. Keep public examples aligned with compiled source examples and exported types; no live Figma write is part of documentation validation. Changes to this contract alone select and force `node-public-docs-test`.

## Change policy

Update the public guides, this contract, the React Forge and public-docs project indexes, the site-selector contract, and relevant `AGENTS.md` rules together when routes, supported interfaces, availability, or validation claims change. Generated `dist` is ignored and removed from the final worktree.
