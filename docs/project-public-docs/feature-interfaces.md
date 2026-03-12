# Feature: interfaces

## Interfaces
Canonical project identifier:

```ts
enum ProjectId {
  PublicDocs = "public-docs",
}
```

Canonical page identifier contract:

```ts
enum PublicDocsPageId {
  Index = "index",
  GettingStarted = "getting-started",
  ProjectsOverview = "projects-overview",
  DocumentationLifecycle = "documentation-lifecycle",
  Devmon = "devmon",
  Nodeup = "nodeup",
  CargoMono = "cargo-mono",
  Derun = "derun",
  DexDex = "dexdex",
}
```

Navigation contract:
- `navigation` must be an object, not an array.
- `navigation.tabs` is the canonical top-level navigation list.
- Tab `Home` must include:
: Group `Get Started` with `index` and `getting-started`.
: Group `Reference` with `projects-overview` and `documentation-lifecycle`.
- Tabs `Devmon`, `Nodeup`, `Cargo Mono`, `Derun`, and `DexDex` must each be present as top-level tabs.
- Tab `Devmon` must include page `devmon`.
- Tab `Nodeup` must include page `nodeup`.
- Tab `Cargo Mono` must include page `cargo-mono`.
- Tab `Derun` must include page `derun`.
- Tab `DexDex` must include page `dexdex`.

Dev preview port contract:
- `pnpm --filter public-docs dev` runs Mintlify with fixed port `46249`.

