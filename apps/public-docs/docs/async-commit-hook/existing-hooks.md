# Existing hooks and hook managers

ach never overwrites another hook. If installation reports a conflict, use the exact command and configuration example it prints. With a custom `core.hooksPath`, ach leaves even an absent hook untouched because that directory may be shared by other repositories. Merge the printed worktree-scoped command into the effective hook. After moving ach or changing its personal configuration path, reinstall the integration.

For a repository-local Lefthook configuration, merge these commands. For shared configurations, use the worktree-scoped example printed by ach:

```
post-commit:
  commands:
    async-commit-hook:
      run: ach run --repo . --commit HEAD --automatic
pre-push:
  commands:
    async-commit-hook:
      use_stdin: true
      run: ach pre-push
```

The pre-push entry is optional. [Lefthook forwards stdin to one consumer](https://lefthook.dev/configuration/use_stdin/); if another check needs push refs too, use one wrapper to save stdin to a private temporary file and replay it for each consumer. Do not let the ach gate receive empty input after another command consumes the refs.

```
#!/bin/sh
set -eu
refs=$(mktemp)
trap 'rm -f "$refs"' EXIT HUP INT TERM
cat > "$refs"
# Replace existing-pre-push with your current stdin-consuming command.
existing-pre-push "$@" < "$refs"
ach pre-push "$@" < "$refs"
```

Put this wrapper behind the hook manager's single `use_stdin: true` entry. Keep your current failure handling and commands. Uninstall removes only unchanged ach-owned files or configuration entries; conflicts remain available for manual reconciliation.
