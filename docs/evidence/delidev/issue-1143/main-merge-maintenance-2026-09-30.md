# Initial PR maintenance: reconcile current main

## Revision and repair

PR #1198 initially reported conflicts at head `46809752b72a4b4f31f26726815d7cea9579a0df`. Merged freshly fetched main `126641a6dcf274420c8c5800a6e88079f0e19990` without rebasing. Reconciled account connection/settings, Settings category copy, independently scoped styles, the desktop contract and project index.

Preserved main's AI API Keys labels and type-scoped entry terminology, Agent Worker presentation/reference, Diagnostics and integrations changes. Retained the compact AI Subscription branch, unavailable lifecycle gates, native metadata pagination/search and both independent style scopes. The new API-copy regression's subscription assertions now expect the issue #1143 empty heading, initially collapsed Advanced controls and absent empty pagination; its API assertions remain intact.

## Validation

- Regenerated the reconciled API client and ran desktop typecheck: passed.
- Warmed the issue-owned Go cache with bounded `go build` after the merge.
- Focused six-file account/subscription/Settings lifetime run passed 70 of 71 tests; the remaining assertion expected the replaced subscription empty copy/pagination. After reconciling it to the documented new UI, the account file passed all 16 tests with default async waits. All 71 focused cases therefore passed across those runs.

- Full `pnpm test` from `apps/delidev` on the reconciled merge: **passed**, exit 0; 91 Vitest files / 1068 tests, eight bundle tests, sixteen desktop launch/asset tests, widget checks and production build. The run used the same temporary two-worker / 15-second test / five-second async waits and isolated bounded Go caches; committed runner files were restored.
- Verified original provider mark notices in production output and removed generated frontend/API-client `dist` directories after the run.

## Limits

Use the temporary issue-owned Go caches and host concurrency/timing allowances described in [the implementation validation](subscription-ui-validation.md); these do not change committed assertions or runner configuration. No packaged native CEF launch, actual native/browser zoom or real-provider lifecycle/quota collection was performed. Merge/build/fixture evidence cannot establish those capabilities.
