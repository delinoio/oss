# Replacement native launch attempt

Date: 2026-09-30. Implementation: `ff9ce5f98311eee00e33ade1d3b3f653d69102bf`, based on `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. Host: macOS 26.6.2 arm64. This is attempted native preparation, not completed CEF acceptance.

Ran the documented `pnpm dev:desktop --data-dir <disposable-temporary-directory>` workflow with a separate temporary Cargo target copied from the existing local cache. Only generated CMake build directories in that copy were removed because their caches retained the original absolute paths. The cache copy reported one disappearing unrelated Forge artifact; it was not treated as a fully consistent snapshot. No source or original target directory was removed.

The launcher verified the hydrated icon, built the generated client and production frontend, prepared the Go sidecar, built both macOS widget extensions successfully and applied ad-hoc signatures. The prepared native desktop host then compiled successfully in 9m45s. The pinned CLI reached its CEF `bundle-and-run` stage and started its desktop build. It was terminated through the launcher with SIGTERM before a completed application launch; the launcher reported that signal and exited 143.

The isolated target grew to about 30 GiB and available host disk space fell from 44 GiB to 13 GiB. The attempt was stopped to reclaim that copied cache, rather than risking further shared-host disk pressure. This deliberate stop is not a product launch failure or a passing packaging check. Generated repository-owned app/client `dist` directories and the copied temporary target were removed afterward.

No new CEF window was inspected or used for screenshots. The full native viewport/200% zoom matrix, native dropdown keyboard behavior, modal focus containment/background inertness/opener restoration, Windows and X11 remain unverified. The actual App's synthetic in-app-browser checks and component results are recorded independently in `replacement-validation.md`; they cannot substitute for packaged CEF acceptance. No real account, provider, user data or external publication was involved in the native attempt.
