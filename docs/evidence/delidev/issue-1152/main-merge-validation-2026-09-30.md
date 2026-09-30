# PR #1196 main merge validation

The first maintenance pass found the PR conflicting with main. This repair
merges `126641a6dcf274420c8c5800a6e88079f0e19990` into the issue branch, preserving the landed GitHub
Integrations and Agent Worker presentation changes.

The conflict was an independent co-addition at the end of `src/styles.css`.
Creation rules now live in `apps/delidev/src/schedule-creation.css`, imported
by the creation component. The merged shared stylesheet is byte-identical
to the fetched main version; both responsive media blocks retain their own
closing braces. Creation selectors, dimensions and behavior remain unchanged.
Scoped AGENTS and the desktop contract identify the component stylesheet owner.

The generated-client build and type check pass. The focused schedule,
Integrations and Agent Worker run passes all three files / 72 assertions.
The production frontend build also passes after stylesheet relocation.
The complete `pnpm test` command from `apps/delidev` passes all 88 files /
1,073 assertions, generated-client build, type checking, eight bundle-verifier
checks, 16 asset/launcher checks, native Swift widget fixtures and production
frontend build. It uses the isolated temporary Go cache and a temporary
one-worker setting restored after execution; no test timeouts or committed
worker settings change. All 102 repository contract checks also pass.
Generated repository-owned dist output is removed after validation.

Earlier browser captures and native limitations remain in the separate
creation validation record; no new native acceptance is claimed.
