# PR #1182 merge maintenance

Date: 2026-09-30. PR head before repair: `84bae14d48a22fec8e7aa7ec663b7a0b94183bc1`. Merged main revision: `7f356266fc195b1880ffac66a93dadab5c5a2df7`. Host: macOS arm64. The previous managed checkout was unavailable; a clean replacement checked out the existing PR branch without copying user changes from the primary checkout.

The only textual conflict was the desktop `AGENTS.md` tail. Preserve both the Activity presentation requirement and Diagnostics ownership. The automatic frontend/desktop-contract reconciliation retains AI API Keys terminology, Diagnostics presentation and the issue #1156 Activity boundary.

Frozen workspace installation and hook installation completed. Generated DeliDev client build and desktop typecheck passed. `pnpm exec vitest run src/activity-sidebar.test.tsx src/settings.test.tsx src/account-settings.test.tsx src/doctor.test.tsx --maxWorkers=1` passed all 84 tests across four files. These are component fixtures, not CEF geometry, native focus or platform acceptance. The original native/responsive limits in `activity-sidebar-validation.md` remain.
