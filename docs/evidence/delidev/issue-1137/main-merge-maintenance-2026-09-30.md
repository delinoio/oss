# PR #1197 initial merge maintenance

The initial issue #1137 head `28e2eb4f78d6404c2c6a49a56b0168cbd9ac6512`
was clean and committed when GitHub reported a conflicting base. On 2026-09-30,
`origin/main` at `126641a6dcf274420c8c5800a6e88079f0e19990` was merged into
the issue branch without rebasing.

Four conflicts were composed from their owning contracts:

- Preserve both scoped app rules, including Integrations and diagnostics ownership.
- Preserve main's AI API Keys and Integrations presentation while retaining the
  stable Diagnostics category ID and Connection & diagnostics label.
- Retain the newer Doctor v2 layout, actual Settings visibility and read-only
  disclosure lifecycle. A closed title enum composes its header with the renamed
  category; standalone Doctor keeps its existing title. Connection controls still
  open the separate persistent panel outside SettingsLifetime.
- Preserve the complete new Agent Worker documentation alongside the launch
  amendment and existing Settings lifetime contract.

The merged client build and UI type check pass. Focused Settings, Doctor, desktop
connection and device-diagnostics checks pass all 69 tests with one worker and a
bounded 15-second test timeout. This covers original report fields/disclosures,
all 16 categories, automatic verified product entry, saved authority and retained
Stop confirmation/receipt behavior. The first merged full run passed 1,052 tests
and failed the category/header title expectation; the composed title fixes that
failure, and the required complete `pnpm test` pipeline is rerun afterward.

Root `cargo test -j 2 -- --test-threads=1` was rerun for the merged tree. It again
fails the two unchanged clibox wait tests with `dns_configuration`, including a
TLS test expecting `tls_certificate`. This remains a whole-workspace validation
limit; no unrelated resolver or TLS behavior was modified.

The [launch evidence](launch-default.md) retains the original native/Go/full
frontend results and explicit platform/service limits. No rendered-native or
other-platform acceptance is inferred from this merge. Generated `dist` output
is removed after the final pipeline, before the repaired worktree is delivered.
