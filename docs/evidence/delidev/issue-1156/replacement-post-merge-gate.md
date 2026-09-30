# Replacement post-merge frontend gate

Date: 2026-09-30. Tested merge: `d69b1fd1b` (main `7090de046` plus the Activity filter implementation). Host: macOS 26.6.2 arm64.

Required `pnpm test` from `apps/delidev` passed the generated client build, TypeScript typecheck, all 1,252 Vitest tests in 97 files, eight bundle-verifier tests, all 16 desktop-launch/asset fixtures, widget checks and production Rsbuild build. A temporary two-worker Vitest limit was restored after the command. This complete run includes the final Activity outlet CSS and main's separate Grok accounting presentation. The focused 45-test composition check is recorded in `replacement-main-7090-merge.md`.

No Go or Rust implementation was manually altered during conflict repair. Generated schema bindings came from consistent main sources without textual conflicts. The final diff against main remains Activity presentation, its scoped instructions/contract and independent evidence. Generated repository-owned app/client `dist` directories were removed after verification. `git diff --check` passed. Native CEF, actual 200% zoom and other-platform acceptance retain the explicit limits in `replacement-native-launch.md` and `replacement-validation.md`; this frontend gate does not establish them.
