# Project: with-watch

## Goal
Provide a Rust-based CLI wrapper that reruns delegated shell utilities and arbitrary commands when inferred or explicit filesystem inputs change.

## Project ID
`with-watch`

## Domain Ownership Map
- `crates/with-watch`

## Domain Contract Documents
- `docs/crates-with-watch-foundation.md`

## Cross-Domain Invariants
- Root passthrough mode must remain `with-watch [--no-hash] <utility> [args...]`.
- Shell mode must remain `with-watch [--no-hash] --shell '<expr>'` and is the supported entrypoint for `&&`, `||`, and `|`.
- Arbitrary command mode must remain `with-watch exec [--no-hash] --input <glob>... -- <command> [args...]`.
- Default change detection must prefer content hashing, while `--no-hash` must switch the rerun filter to metadata-only comparison.
- `exec --input` reruns the delegated command unchanged and must not inject changed paths into argv or environment variables.

## Change Policy
- Update this index and `docs/crates-with-watch-foundation.md` together when CLI shape, watch inference behavior, or storage/logging contracts change.
- Keep root `AGENTS.md` and `crates/AGENTS.md` aligned with ownership and project-ID changes.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
