# Feature: architecture

## Architecture
- Top-level command router dispatches to rustup-style subcommand groups (`toolchain`, `show`, `override`, `self`) and leaf commands (`default`, `update`, `check`, `run`, `which`, `completions`).
- Version resolver normalizes user input into a canonical runtime selector (exact version and stable aliases such as `lts`, `current`, `latest`).
- Release index resolver caches channel metadata on disk (`cache/release-index.json`) with a default TTL of 10 minutes and stale-cache fallback when refresh fails.
- Runtime installer/downloader fetches official Node.js archives, validates `SHA256` checksums from `SHASUMS256.txt`, and stages verified artifacts before activation.
- Runtime store manager maintains installed runtimes, linked runtime metadata, tracked selectors, and activation pointers.
- Override manager resolves runtime precedence by directory scope and fallback defaults.
- Shim dispatcher handles executable-name-based mode branching for `node`, `npm`, `npx`, and other managed aliases.
- Self-management module implements update/uninstall/data-migration flows with deterministic status outputs and action/outcome logs.
- Completion module remains an explicit skeleton command returning deterministic `NotImplemented`.

