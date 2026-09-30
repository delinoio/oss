# Native CLI baseline control

Feature implementation: `fc0abfb307444d7dde54cc94bf3bf54ed82171c2`.
Untouched-main control: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.
Executed on 2026-09-30 using independent temporary repositories, accounts and
native process scopes; no user credentials were used.

The required `go test -race -timeout=20m -p=2 ./cmds/delidev-cli/...` run recorded a
failure in `TestCLISessionAcceptanceQueueAndArchive` after 40.53s: the native
workspace reader was unavailable during `session review create` at
`sessions_test.go:258`. Its CLI package finished failed at 147.239s. The full run
continued through other packages; this record does not report it as completed or
passing. Final complete results are to be recorded separately.

A separate feature-branch single-test rerun also failed, after 42.47s at the
workspace `session files read` boundary (`sessions_test.go:215`).

To distinguish the changed CI evaluator from existing native fixture limitations,
exported the exact untouched main revision with `git archive` into an independent
scratch directory and ran:

```sh
go test -race -timeout=20m ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1
```

The control failed after 59.27s (package 60.732s), at the earlier `session create
--wait` workspace-preparation timeout (`sessions_test.go:202`). This establishes
that the existing fixture also fails on untouched main in this environment. The
different failing stages do not prove that every feature-branch native failure
has the same cause. No native acceptance or passing complete suite is inferred.

Focused authenticated PR query/problem/remediation RPC and CLI race regressions
passed independently: server 27.322s, CLI 8.688s. The ALLGREEN domain/provider/store
regressions and full desktop suite are recorded in
[replacement validation](restored-allgreen-validation.md).
