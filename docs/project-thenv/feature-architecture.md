# Feature: architecture

## Architecture
- CLI (`cmds/thenv`) handles local workflows:
: Local file parse (`.env`, `.dev.vars`), push orchestration, pull file materialization, and conflict enforcement.
- Server (`servers/thenv`) handles business flows over Connect RPC:
: Bundle version storage, active pointer state, policy enforcement, envelope encryption/decryption, and audit event persistence.
- Web console (`apps/devkit/src/apps/thenv`) handles management and visibility:
: Version inventory, active version switching, role policy management, and audit browsing without secret value rendering.
: Audit browsing renders outcome (`SUCCESS`, `DENIED`, `FAILED`, `UNSPECIFIED`) and supports optional `fromTime` / `toTime` range filtering.
: Version inventory and audit browsing support cursor-based pagination via explicit "Load More" controls until `nextCursor` is empty.
- Devkit API routes (`apps/devkit/src/app/api/thenv/*`) proxy web requests to Connect RPC procedures.

Trust boundary and plaintext handling:
- Plaintext is allowed in CLI process memory when reading local source files and writing pulled output files.
- Plaintext is allowed in server process memory only during authorized encrypt/decrypt paths.
- Plaintext is not allowed in persistent server storage, logs, metrics labels, frontend state, or browser storage.

Communication boundary:
- Business flows use Connect RPC between clients (CLI/web backend adapters) and `servers/thenv`.
- Tauri-specific bindings are not part of the thenv business contract.

