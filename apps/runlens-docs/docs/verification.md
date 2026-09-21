# Clean and repeated verification

## Clean execution

```sh
runlens verify clean build --save clean.json
runlens verify clean build --baseline local.json --save comparison.json
runlens verify clean build --include-working-tree --save edited.json
```

Runlens selects the repository's HEAD commit by default and reports that uncommitted changes were excluded. `--include-working-tree` includes tracked changes and nonignored untracked files. The selected source is frozen before execution.

The command runs in a temporary checkout, never by changing or rerunning it in your current worktree. HOME, configuration directories, and common caches are fresh. Only required OS execution context and explicitly named environment variables are passed. Credentials, ambient user configuration, package installation, submodule initialization, and other setup are not inferred or copied automatically.

Configure preparation steps explicitly. Every step must succeed before the target starts. A missing dependency or credential is a setup failure, not a reason to copy the user's environment.

Global read/write policy boundaries also apply to preparation steps. Input/output
declarations and baseline new-access checks apply to the target command.

Without a baseline, success means clean execution and applicable configured checks passed. It does **not** establish equivalence to your current worktree.

A baseline must use the same source revision and working-tree inclusion policy as the clean run. Different source selections make the comparison inconclusive, even when observed file contents match.
Both reports need a known source revision. Runs outside Git, or with failed revision discovery, remain valid receipts but cannot certify baseline compatibility, even when both revisions are missing.
If the current run violates a policy, verification remains failed (exit 5) even when its baseline comparison is inconclusive. Without a definite failure, inconclusive evidence returns exit 4.

When a command selects environment variables, reports retain their names only. Even identical names cannot establish that the values matched, so baseline comparison and repeated-output compatibility remain inconclusive. This also applies when the values happened to be equal; Runlens does not persist values or hashes of them as proof. A clean run without a baseline can still pass its execution and policy checks.

## Repeated outputs

```sh
runlens verify repeat build --save repeated.json
runlens verify repeat build --runs 5 --save five-runs.json
```

The default is three repetitions; explicit counts range from 2 to 32. Output declarations are required. Each repetition starts from the same frozen source in a new checkout, HOME, and cache, and repeats the explicit preparation.

Runlens compares declared output path sets, types, SHA256 content hashes, symlink metadata, and applicable executable permissions. Missing outputs, changed bytes, changed path sets, permissions, failed commands, or unknown evidence cannot produce a passing verification. Timestamps alone are not file-content differences.

Equal observed outputs describe those executions only; they do not prove universal determinism. Network activity, clocks, randomness, and environment-variable reads are outside filesystem observation.

## Cancellation and isolation limits

Configure timeouts explicitly; there is no automatic retry. Cancellation and lingering children receive a five-second termination grace period before forced termination and reaping. Incomplete evidence remains classified as incomplete. Cleanup failures produce a tool failure with recovery guidance.

Temporary checkout isolation is operational isolation, **not an OS security boundary**. Preparation and target commands retain their host permissions and network access. Use trusted finite commands; background services, PTYs, and interactive sessions are unsupported.

Every repetition preserves file, directory and symlink access/modification timestamps from the frozen source. HEAD files use the timestamps of the initially selected checkout because Git does not store worktree timestamps. Creation/change times and inode identities are not reproduced.

If cleanup fails after a completed repetition, Runlens stops further repetitions and retains completed execution evidence for `--save`, including the actual child result. The report records a cleanup failure and cannot pass verification.
