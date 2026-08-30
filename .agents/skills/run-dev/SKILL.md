---
name: run-dev
description: Start the complete local DevHud frontend, administrator, and API stack in Delino or OSS mode. Use only when the user explicitly invokes $run-dev; do not activate for ordinary development-server requests.
---

# Run Dev

Start the repository's complete local DevHud stack. Do not use this skill for documentation-only or package-scoped development servers.

## Select the credential mode

- Accept an explicitly requested `Delino`, `team`, or `OSS` mode. Treat `Delino` and `team` as the same mode.
- If the invocation does not specify a mode, ask one blocking question with exactly these choices: `Delino` and `OSS`. Do not start anything until the user chooses.
- Do not ask again when the invocation already identifies one mode unambiguously.

Run every command from the repository root. Do not change modes automatically after a failure.

## Prepare workspace dependencies

After the user selects a mode and before running its commands, check the repository-root workspace dependencies:

- If the root `node_modules` directory is absent, run `pnpm install` once and wait for it to succeed.
- If `node_modules` is present, do not install dependencies routinely. If a mode command later fails with an error that clearly identifies a missing workspace module or workspace binary, run `pnpm install` once if it has not already run during this invocation, then retry only the failed command once.
- Stop immediately if installation fails, if the retry fails, or if dependency installation already ran during this invocation. Do not repeat installation, restart the full sequence, or change modes.
- Treat `pnpm install` as permission to prepare only the repository's pnpm workspace dependencies. It does not authorize installing or upgrading pnpm itself, Infisical, Docker, Go, Rust, or any other external tool.

## Delino mode

Run these commands sequentially, waiting for each command to exit successfully before starting the next:

```bash
pnpm env:login
pnpm env:doctor
pnpm dev
```

Except for the single missing-workspace-dependency recovery above, stop immediately when any command fails. `pnpm env:login` owns interactive authentication and local Infisical project initialization; `pnpm env:doctor` owns the non-mutating readiness check; and `pnpm dev` owns service validation, migration, and startup.

Never invoke raw Infisical synchronization, export provider values, write them to `.env` files, or place them in the root or Turbo environment. The existing service-owned wrappers inject and validate the current allowlisted values. Do not fall back to OSS mode.

## OSS mode

After workspace dependency preparation, run:

```bash
pnpm dev:oss
```

Do not invoke Infisical, `pnpm env:login`, or `pnpm env:doctor` in OSS mode. The existing command owns local dependency startup, migration, application startup, and cleanup.

## Process and failure handling

- Keep the selected development command attached to a persistent terminal session. Do not detach or background it.
- Once startup is healthy, report that the frontend, administrator, and API use fixed ports `46305`, `46306`, and `46307` respectively.
- When the user asks to stop, interrupt the active command and wait for its normal cleanup to finish.
- Surface the command's sanitized failure category. Do not expose credentials or raw provider output.
- Outside the permitted repository-root `pnpm install`, do not install or upgrade tools, kill port owners, edit environment files, remove Docker volumes, or perform other remediation unless the user explicitly requests it.
