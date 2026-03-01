# GitHub PR Codex Review Loop Reference

## Purpose

Use this reference while running the loop defined in `../SKILL.md`.
It provides deterministic commands for actor matching, approval checks, and review-thread operations.

## Actor Matching

Default Codex actors in bundled scripts:

- `codex`
- `codex[bot]`
- `openai-codex`
- `openai-codex[bot]`
- `chatgpt-codex-connector[bot]`

If your workspace uses a different identity, pass `--actor <login>` or `--actor-regex "<pattern>"`.

## Approval Check (Manual Command)

```bash
gh api repos/OWNER/REPO/issues/PR_NUMBER/reactions \
  --jq '.[] | select(.content == "+1") | "\(.user.login)\t\(.created_at)"'
```

Interpretation:

- If at least one matching actor appears, treat the PR as Codex-approved.
- If no matching actor appears, continue the loop.

## Review-Thread Listing (GraphQL)

Use this query to list unresolved review threads and their latest comment metadata:

```bash
gh api graphql \
  -f owner='OWNER' \
  -f repo='REPO' \
  -F number=PR_NUMBER \
  -f query='
    query($owner: String!, $repo: String!, $number: Int!) {
      repository(owner: $owner, name: $repo) {
        pullRequest(number: $number) {
          reviewThreads(first: 100) {
            nodes {
              id
              isResolved
              isOutdated
              comments(last: 1) {
                nodes {
                  author { login }
                  body
                  url
                }
              }
            }
          }
        }
      }
    }
  '
```

## Review-Thread Resolve Mutation (GraphQL)

Resolve a thread only after the corresponding feedback is fully addressed:

```bash
gh api graphql \
  -f query='
    mutation($threadId: ID!) {
      resolveReviewThread(input: { threadId: $threadId }) {
        thread { id isResolved }
      }
    }
  ' \
  -f threadId='THREAD_ID'
```

## Operational Guardrails

- Do not resolve threads preemptively.
- Do not stop the loop on `APPROVED` review state alone; use the `:+1:` reaction as the stop condition.
- Keep iteration commits focused so each review round remains explainable.
