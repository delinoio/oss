# Commands

## Observe a command

```sh
runlens run -- your-build-tool build
runlens run --command build --save build.json
runlens run --timeout-ms 60000 --save test.json -- your-test-tool
```

Each command runs once; Runlens never retries it automatically. Stdout/stderr remain the child's streams. Finite piped stdin is forwarded; terminal stdin receives EOF because interactive prompts and PTYs are outside scope. Runlens progress and execution summaries use stderr. `--log-level off` suppresses structured diagnostics; `--color never` disables color.

## Compare executions

```sh
runlens compare local.json ci.json --json
runlens compare local.json ci.json --map '${home}/ci-tools=${home}/tools'
```

Mappings apply to right-hand paths and must not create duplicate identities. Comparisons expose accesses, before/after states, outcomes, and environment differences. Different OSes, architectures, engine versions, or command environments cannot establish reproducibility or cache safety. A completed comparison is not itself an equivalence claim.

## Audit declarations and policy

```sh
runlens cache check build.json --command build
runlens policy check build.json --baseline baseline.json
```

Findings identify undeclared inputs, uncovered outputs, overlap, disallowed reads/writes, and newly observed accesses. An attempted read is not proof of successful content access. Declarations describe the command selected in configuration.

## Read a receipt

```sh
runlens receipt build.json
```

A receipt separates access attempts from snapshot-proven changes, including created, modified, deleted, and type-changed paths. Unknown states remain visibly unknown.

## Explain files and potential conflicts

```sh
runlens explain src/main.rs --report build.json --report test.json
runlens conflicts --report build.json --report test.json --json
```

File usage may indicate a producer or consumer candidate. Overlapping writes or read/write relationships indicate potential conflicts; they do not prove causality, event order, or a race. These commands never execute recorded commands or search hidden history.

## Verify and share

Use [clean and repeated verification](/verification) for fresh executions, [JSON and offline HTML export](/reports) to share results, and `runlens doctor --json` to inspect prerequisite availability. Read-only analysis commands accept `--json` and keep stdout free of progress messages.
