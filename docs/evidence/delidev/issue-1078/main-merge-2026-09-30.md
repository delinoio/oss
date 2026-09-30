# PR #1171: main merge maintenance on 2026-09-30

## Source and resolution

The final repair inventory reported merge conflicts after main advanced during
validation. Merge main `b1b3e9e7c55511086a284021850426d48484b127` into the existing
PR branch at `d35e41e59eb102f7759bc0fca14f895feef1338a`; no rebase or new branch.
The CI activity-erasure repair remains its separate source commit `4948d23b`.

The three textual conflicts were appended instructions in the DeliDev domain,
server and Worker AGENTS files. Retain both the permanent-deletion ownership and
capacity rules and main's independently verified failed-Claude continuation,
explicit Resume, paused-dispatch and lost-report restrictions. Do not choose
one side's policies over the other. Main's source/protocol/frontend changes
remain intact; deletion admission still rejects new copies and resumed dispatch.

Update the activity contract to describe implemented permanent erasure instead
of future cleanup. Original attempt-source reservation activity is removed before
shared attempt operands are redacted, without publishing a replacement activity;
shared provenance and lifetime counters remain retained. This composes with
main's ordinary activity hook and session-owned activity cleanup.

## Validation on the merged worktree

With `GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4`:

- `go test -race -p 2 ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/domain
  -run 'SessionDeletion|PRActivity|PRRemediation|ClaudeFailed|FailedResume'
  -count=1` passed: store 45.226 s, Worker 22.075 s, server 54.198 s,
  domain 2.702 s.
- `go vet -p 2 ./cmds/delidev-cli/...` passed.
- `pnpm proto:check` passed lint, compatibility/breaking and regenerated-output
  freshness checks. Generated Go and TypeScript files match the merged index.
- `pnpm test` from `apps/delidev` passed API-client build, TypeScript checks,
  89 Vitest files / 1,122 tests, bundle dry-run, 16 desktop-launch checks, widget
  fixtures and production frontend build.
- The exact tracked DeliDev icon LFS object was hydrated; `git lfs ls-files`
  reports restored content. LFS reported that its index refresh awaited the
  three unresolved merge entries; the resolved files are staged for the merge.
- Generated `apps/delidev/dist` and `packages/delidev-api-client/dist` are removed
  after validation. No Rust source was changed by this repair or merge.

The earlier required root Go race command and exact CI-merge store results are
recorded in `ci-activity-composition.md`: the complete store suite passed, while
the root race command failed at the previously documented CLI creation-diff
`unavailable` fixture. Do not infer a full root-suite pass or a new unchanged-base
reproduction. These are local fixture/build checks, not native provider or
cross-platform distribution acceptance.

## Publication and review limits

Keep the existing PR's `Closes #1078` reference and publish once after the merge
commit. New CI results belong to the newly published head. Codex review credits
remain exhausted according to the existing connector comment; repository-admin
action is required before fresh review can run. Do not merge or enable auto-merge.
