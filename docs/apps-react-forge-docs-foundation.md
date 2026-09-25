# React Forge public documentation

## Scope

`apps/public-docs/docs/react-forge` owns the English user guides at `https://oss.delino.io/react-forge/`. The existing `public-docs` Rspress app builds and publishes this section with the other major projects. There is no separate documentation workspace, hosting target, or development port.

## Audience and source of truth

Node.js 24 developers using the public `@delino/react-forge` library or CLI and trusted local MCP clients are the primary audience. Curate from `docs/project-react-forge.md`, the package, Figma, static-scene, MCP, release, and validation contracts, the public types, and the npm README. Preserve source-backed behavior and limitations. Keep architecture, repository paths, release operations, and evidence reproduction commands in `docs/`; public pages describe supported user workflows, stable interfaces, availability, and honest validation limits.

## Routes and content

The stable clean routes are `/react-forge/`, `/installation`, `/getting-started`, `/sessions`, `/formats/pptx/`, `/formats/docx/`, `/formats/xlsx/`, `/formats/pdf/`, `/office-editing`, `/formats/figma/`, `/cli`, `/mcp`, `/formats/glb/`, `/formats/fbx/`, `/formats/sfx/`, `/formats/sprite/`, `/limits-and-troubleshooting`, and `/releases`, all under `/react-forge`. The nine format guides live in `apps/public-docs/docs/react-forge/formats/<format>/index.md`; each format has its own directory.

The former format routes `/pptx`, `/docx`, `/xlsx`, `/pdf`, `/figma`, `/sfx`, `/sprite`, `/glb`, and `/fbx` under `/react-forge` redirect permanently to their matching `/formats/<format>/` routes, including trailing-slash variants. New links and navigation use only the new routes. This same-origin migration does not add retired standalone-host aliases.

- The landing, installation, and getting-started pages give new developers a direct path from package installation to a first PPTX export, then to a format or workflow guide. The React Forge sidebar groups start, document formats, editing/automation, unreleased previews, and help without changing other project navigation.
- Installation and getting started cover Node.js 24, six local-document host combinations, optional native packages, fonts, and an executable TSX task.
- Sessions and format guides cover the public React/session APIs, all four local document models, explicit exports, Office preservation, font/layout limits, and the absence of PDF import or Office conversion.
- GLB and FBX guides describe the unreleased generation-only SceneSession API, registered binary assets, world-space bounds, Blender 4.5 material profile, resource limits, and exclusions. They explicitly state that npm 0.1.1 does not include this extension; hosted-platform checks are not implied by local evidence.
- SFX documents offline procedural WAV generation, bounded layers, timing and seeded variation with a zombie-game gunshot example. Mark this extension unreleased and absent from npm `0.1.1`; keep historical document validation distinct.
- Sprite previews source-backed pixel drawing and animation, the revision-pinned `.sprite.zip` archive, bounds, and generation-only limits. Mark it unreleased and absent from npm `0.1.1`; do not imply engine importer compatibility or reuse historical document validation as Sprite evidence.
- Figma explains its narrower macOS Keychain authentication boundary, official MCP connection, explicit remote publication, complete/partial/unknown receipts, and recovery without blind retries. Receipts may omit a file locator after ambiguous creation; guide readers to establish the remote file's identity before reopening or repeating any write.
- CLI and MCP guides distinguish one-shot tasks from retained in-memory sessions and explain cancellation, trust, output isolation, and explicit publication.
- Limits and releases document typed recovery, resource ceilings, first functional npm version `0.1.1`, six-host local-document validation, and the absence of direct Microsoft Office validation or PDF/UA certification. Current install guidance should resolve npm's published dist-tag instead of claiming an old version is perpetually latest.

Every route appears in the Rspress sidebar and the shared selector identifies `/react-forge` as React Forge. The root landing page and project catalog link to the canonical same-origin section; the npm README points there. No duplicate root guide or handoff route is added.

## Build and validation

Use the consolidated `pnpm dev:public-docs` loopback server on port `46302` and the existing Cloudflare Pages publication. Run `pnpm test` from `apps/public-docs`. Rendered validation checks all 18 artifacts, headings and guide links, sidebar entries, exact selector state, repository links, clean routes, the nine format redirects, and prohibited credential/internal-path content. Validate that the GLB, FBX, SFX and Sprite article introductions explicitly identify their absence from npm `0.1.1`. Keep public examples aligned with compiled source examples and exported types; no live Figma write is part of documentation validation. Changes to this contract alone select and force `node-public-docs-test`.

## Change policy

Update the public guides, this contract, the React Forge and public-docs project indexes, the site-selector contract, and relevant `AGENTS.md` rules together when routes, supported interfaces, availability, or validation claims change. Generated `dist` is ignored and removed from the final worktree.
