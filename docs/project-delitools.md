# Project: DeliTools

## Goal

Provide an executable blank shell like DeliDev, with desktop, Android and iOS hosts and a mobile distribution pipeline.

## Project ID

`delitools`. The npm workspace is `delitools`; the native package is `delitools-shell`.

## Domain Ownership Map

- `apps/delitools`: shared empty React frontend, CEF desktop host and Wry mobile host.
- `.github/workflows/release-delitools-mobile.yml`: signed Android internal testing and iOS TestFlight uploads.

## Domain Contract Documents

- [Application foundation](apps-delitools-foundation.md)

## Cross-Domain Invariants

All hosts render an empty root with the `DeliTools` application name and `io.delino.delitools` identifier. There is no product backend, account access, RPC or product storage. DeliDev remains a separate application. React, Rsbuild and Rust follow the existing DeliDev shell instead of the default new-project Go language. Mobile uses system webviews; CEF is desktop-only.

## Change Policy

Update this index and the application contract together when paths, targets or release boundaries change. Keep validation results and unperformed native/store checks in commit, PR or CI records. Update instructions only for ownership and development-rule changes.

## References

- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [DeliDev](project-delidev.md)
