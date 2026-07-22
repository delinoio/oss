---
name: write-prd
description: Decision-complete PRD drafting and GitHub issue filing for repository features. Use only when a human explicitly invokes `$write-prd`; never select this skill automatically for similar feature, planning, PRD, or issue tasks. When invoked, ground a feature request in repository contracts, close a decision ledger, prepare an implementation-ready PRD, and file it in the current GitHub repository without labels or other issue metadata.
---

# Write PRD

## Goal

Turn a feature request into a decision-complete PRD and GitHub issue. Do not invent product or operational defaults. Close every required decision with a user answer, an explicit user-approved `not applicable`, or direct repository-contract evidence before filing the issue.

## Required References

Read both references before asking questions or drafting the issue:

- `references/question-ledger.md` defines the required decisions and ledger states.
- `references/issue-template.md` defines the issue title, body, and filing checks.

## Operating Rules

- Follow all active system, developer, repository, and scoped `AGENTS.md` instructions.
- At the start of the task, list `docs/`, then read the root and applicable scoped `AGENTS.md` files and the relevant `docs/` contracts.
- Resolve discoverable repository facts before asking the user. Record direct evidence as `contract-determined`; ask only when product intent or a genuine tradeoff remains open.
- Ask focused questions in small batches. Reconcile conflicting answers immediately.
- Keep facts, user decisions, and remaining questions distinct in the ledger.
- Resolve the current GitHub repository with `gh repo view --json nameWithOwner -q .nameWithOwner`. If it cannot be resolved unambiguously, ask for the target before continuing.
- Search the current repository for duplicate or overlapping issues before finalizing the PRD.
- Do not set labels, assignees, milestones, project fields, issue types, or any other issue metadata. If an applicable repository contract requires metadata, report the conflict and stop before filing.
- Do not file while any required ledger row is `open` or while answers contradict repository contracts.

## Workflow

1. Ground the feature.
   - Identify the requested outcome, affected project and domain, target users, and current repository.
   - Inspect applicable contracts, nearby implementation, schemas, APIs, routes, and existing issues.
   - Initialize a visible ledger from `references/question-ledger.md`, recording contract evidence immediately.

2. Close the ledger.
   - Ask only questions that materially close one or more open rows.
   - Accept `not applicable` only when the user explicitly chooses it and provides enough context to preserve the boundary.
   - Repeat discovery and questioning until every required row is closed and internally consistent.

3. Handle Plan Mode.
   - When Plan Mode is active, do not create an issue or mutate external state.
   - Produce a complete plan containing the exact issue title, full body, resolved repository name, and the later `gh issue create` action without metadata flags.
   - When executing a complete prior plan outside Plan Mode, file the issue without requesting an additional confirmation.

4. Draft the issue.
   - Follow `references/issue-template.md` and the current repository's issue contract.
   - Translate closed ledger decisions into concrete scope, observable acceptance criteria, test scenarios, and explicit exclusions.
   - Omit unsupported claims and implementation details that remain undecided.

5. File the issue.
   - Outside Plan Mode, write the multiline body to a safely created temporary file.
   - Run `gh issue create` against the resolved repository with only `--repo`, `--title`, and `--body-file`.
   - Report the issue URL, title, repository, and any unresolved follow-up risk.

## Useful Commands

```bash
gh repo view --json nameWithOwner -q .nameWithOwner
gh issue list --repo "$OWNER_REPO" --search "$SEARCH_TERMS" --state open
gh issue create --repo "$OWNER_REPO" --title "$TITLE" --body-file "$BODY_FILE"
```

Quote shell variables and paths. Use a safely created temporary file for multiline Markdown and remove it after the command completes.
