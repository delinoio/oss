---
name: batch-mode
description: Apply additional CI constraints only when a human explicitly invokes $batch-mode or requests Batch Mode. Do not activate automatically for batch work or CI tasks.
---

# Batch Mode

Apply these additional instructions to the current task only when a human explicitly invokes this skill:

- Do not start manual CI or trigger `workflow_dispatch`.
- Use the existing PR's automatic CI.
- Retrying a failed CI run is allowed.
- Never modify `.github/workflows/CI.yml`.
- Prioritize issues assigned to [Batch group 1](https://github.com/delinoio/oss/milestone/1) over issues assigned to [Batch group 2](https://github.com/delinoio/oss/milestone/2).

This skill adds instructions only. It does not start batch work or create a PR.
