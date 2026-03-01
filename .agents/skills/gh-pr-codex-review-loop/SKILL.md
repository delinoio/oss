---
name: gh-pr-codex-review-loop
description: Repeatedly address GitHub pull request feedback until Codex marks the pull request as accepted with a :+1: reaction. Use when a user asks to iteratively apply PR review comments, run validations, push follow-up commits, and continue until Codex auto review is satisfied.
---

# GitHub PR Codex Review Loop

## Overview

Run a deterministic loop for PR hardening:
1. Check whether Codex already reacted with `:+1:`.
2. If not approved, gather Codex-authored feedback.
3. Implement fixes, run validations, commit, and push.
4. Repeat until approval is present.

Use GitHub CLI (`gh`) for all GitHub operations.

## Required Inputs

- PR number or PR URL.
- Repository slug (`owner/repo`) when working outside the checked-out repo.
- Optional Codex actor names when your organization uses a custom bot identity.

## Loop Workflow

1. Check approval signal:
```bash
node scripts/check_codex_thumbs_up.js 123 --repo owner/repo
```
2. Stop immediately if `approved=true`.
3. Collect Codex feedback context:
```bash
node scripts/collect_codex_feedback.js 123 --repo owner/repo --format markdown
```
4. Address all active feedback in a single coherent fix set.
5. Run required project validations before commit.
6. Stage and commit without delay after staging:
```bash
git add <changed-files>
git commit -m "fix: address codex PR feedback"
git push
```
7. Resolve addressed review threads:
   - Inspect unresolved threads with `gh api graphql` query from `references/gh-review-loop-reference.md`.
   - Resolve only threads fully addressed by the push.
8. Repeat from step 1 until approval is detected.

## Validation Rules

- Stop the loop only on explicit `:+1:` reaction by a Codex actor.
- Do not claim success when feedback is missing but approval is absent.
- Keep each iteration small and reviewable.
- Preserve unrelated local changes.

## Helper Scripts

- `scripts/check_codex_thumbs_up.js`
  - Exit `0` when approval is detected.
  - Exit `1` when approval is not detected.
  - Print JSON summary for logs and automation.
- `scripts/collect_codex_feedback.js`
  - Gather Codex-authored review summaries, inline comments, and discussion comments.
  - Output `markdown` (default) or `json`.

## Troubleshooting

- Read `references/gh-review-loop-reference.md` for:
  - Actor matching strategy.
  - GraphQL snippets for review-thread listing and resolution.
  - Operational guardrails for ambiguous feedback states.
