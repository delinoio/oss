---
name: repair-pr
description: One-shot pull request repair workflow for this repository. Use when asked to repair the current or specified PR by resolving merge conflicts, applying actionable unresolved review feedback, resolving handled review threads, fixing failing CI, committing coherent repair chunks, and pushing once at the end.
---

# Repair PR

## Goal

Repair the current or specified GitHub PR once, then stop. Handle merge conflicts, actionable unresolved review feedback, and CI failures in that order. Commit after each coherent repair phase, but push only once after all local changes are complete.

## Workflow

1. Resolve the PR context.
   - Read the repository `AGENTS.md` before changing code.
   - List files in `docs/` before starting work, and use `docs/` as the source of truth for project contracts.
   - Confirm `gh auth status` works.
   - If the user provided a PR number or URL, use it; otherwise use `gh pr view --json number,url,baseRefName,headRefName`.
   - Run `node .agents/skills/repair-pr/scripts/repair-pr.mjs status --pr <pr>` to collect merge state, unresolved review threads, and failing checks.
   - Use `--review-author <login>` only when the user or task scope asks to focus on a specific reviewer or bot.

2. Resolve merge conflicts first.
   - Treat `mergeStateStatus: DIRTY` or GitHub reporting conflicts as the conflict signal.
   - Fetch the PR base branch and merge it into the PR branch; do not rebase.
   - Use `git merge origin/<baseRefName>` or the correct remote-tracking base ref for this repository.
   - Resolve conflicts by reading source, fixtures, tests, `AGENTS.md`, and relevant `docs/` contracts. Do not choose `--ours` or `--theirs` blindly.
   - Run focused verification for the resolved area, then `git add` the intended files and commit the merge or conflict repair before moving on.

3. Apply review feedback.
   - Consider only unresolved, non-outdated review threads from the helper output.
   - Ignore approvals, resolved threads, outdated threads, duplicates, and non-actionable notes.
   - Group related actionable threads by behavior or file, implement the smallest correct fix, and update relevant `docs/` or `AGENTS.md` files when contracts, policies, or structure changed.
   - Run focused tests for each repair group.
   - Commit each coherent review-fix group.
   - After the relevant fix is committed, resolve each handled thread with `node .agents/skills/repair-pr/scripts/repair-pr.mjs resolve-thread <thread-id>`.
   - If a review comment is ambiguous or would cause a regression, leave the thread unresolved and report the blocker.

4. Fix CI failures.
   - Use `gh pr checks <pr> --json name,state,bucket,link,workflow` to identify failing checks.
   - For GitHub Actions failures, inspect logs with `gh run view <run-id> --log` or job logs from `gh api` when needed.
   - Treat external checks as report-only unless their logs are available through `gh`.
   - Fix the observed root cause, run focused local verification, and commit the CI fix.

5. Finish once.
   - Run repository-required verification from `AGENTS.md` when feasible. At minimum, run checks for the touched domains: `cargo test` from the root after Rust changes, `pnpm test` from the relevant frontend directory after frontend changes, and the closest focused checks for other domains.
   - Re-run `node .agents/skills/repair-pr/scripts/repair-pr.mjs status --pr <pr>` once for a final summary.
   - If any commits were created, push once with `git push` for the current branch. Because this workflow merges instead of rebasing, do not force-push.
   - Do not start a monitoring loop or keep polling checks after the final status check.

## Helper

Use the helper from the repository root:

```bash
node .agents/skills/repair-pr/scripts/repair-pr.mjs status
node .agents/skills/repair-pr/scripts/repair-pr.mjs status --pr 123 --json
node .agents/skills/repair-pr/scripts/repair-pr.mjs status --pr 123 --review-author chatgpt-codex-connector[bot]
node .agents/skills/repair-pr/scripts/repair-pr.mjs resolve-thread PRRT_kwDO...
```

The helper is an inventory and review-thread mutation aid. It does not implement code fixes, stage changes, commit, push, or decide whether a review comment is correct.

## Commit And Push Rules

- Commit after each coherent phase that changes files: merge-conflict repair, review repair group, CI repair group.
- Stage only files that belong to the current repair.
- Push exactly once at the end if at least one commit was created.
- If no local changes were needed, do not create an empty commit and do not push.
