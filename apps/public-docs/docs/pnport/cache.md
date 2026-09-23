# Cache management

**pnport is unreleased; these commands describe the intended first public version.**

pnport materializes package bytes lazily into a private user cache when supported native access needs ordinary files. It never creates a project `node_modules` directory. Completed entries are kept until you explicitly clean them; there is no automatic expiry, eviction, or product size quota.

Use `pnport cache path` to locate the active cache and `pnport cache list` to inspect entries and state. `pnport cache prune` removes abandoned incomplete entries and obsolete cache formats. `pnport cache clean` removes inactive entries. Both must retain entries used by active runs and report deletion failures rather than removing unrelated files. `--cache-dir` selects a different private cache for a command.

Disk exhaustion and permission errors are recoverable failures. Different installed pnport versions keep incompatible cache formats separate so a fixed-version rollback does not destructively migrate a newer cache. See [diagnostics](/pnport/diagnostics) before removing files by hand.
