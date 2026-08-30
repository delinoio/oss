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

## Delino mode

Run these commands sequentially, waiting for each command to exit successfully before starting the next:

```bash
pnpm env:login
pnpm env:doctor
pnpm dev
```

Stop immediately when any command fails. `pnpm env:login` owns interactive authentication and local Infisical project initialization; `pnpm env:doctor` owns the non-mutating readiness check; and `pnpm dev` owns service validation, migration, and startup.

Never invoke raw Infisical synchronization, export provider values, write them to `.env` files, or place them in the root or Turbo environment. The existing service-owned wrappers inject and validate the current allowlisted values. Do not fall back to OSS mode.

## OSS mode

Run only:

```bash
pnpm dev:oss
```

Do not invoke Infisical, `pnpm env:login`, or `pnpm env:doctor` in OSS mode. The existing command owns local dependency startup, migration, application startup, and cleanup.

## Process and failure handling

- Keep the selected development command attached to a persistent terminal session. Do not detach or background it.
- Once startup is healthy, report that the frontend, administrator, and API use fixed ports `46305`, `46306`, and `46307` respectively.
- When the user asks to stop, interrupt the active command and wait for its normal cleanup to finish.
- Surface the command's sanitized failure category. Do not expose credentials or raw provider output.
- Do not install or upgrade tools, kill port owners, edit environment files, remove Docker volumes, or perform other remediation unless the user explicitly requests it.
