# Claude task-backed child creation

Inspected PR #1225 head `490874cd554a6aa0130c00fc8c4726b5bc200e35` and Codex
review thread `PRRT_kwDORRAKg86nwlG9` (comment 4150644442). The original server
accepted a new Claude child from content/history when its parent tool was valid,
without first requiring the original local-agent task. The Worker already required
an original task start; server publication now independently requires task source
and non-null task metadata when native ownership is absent. Existing content/history
updates still use exact retained ownership and the original parent-tool check.

The real authenticated server/SQLite regression places a valid task-backed sibling
before each invalid creation. Content/history with and without copied task metadata,
and task source without task metadata, all reject the whole batch without changing
the session revision or retaining either child. A subsequent valid sibling can reuse
the unconsumed sequence. Positive coverage retains root task, content, nested task,
nested content, history and exact receipt replay. The existing omission regression
now first establishes task ownership and checks all three original source entries.

At the inspected head plus the new regression, the following red command failed
all five negative cases because publication incorrectly succeeded; its positive
case passed (server 5.165 s):

```sh
GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/server -run '^TestSubagentClaude(CreationRequiresOriginalTaskAtomically|TaskCreationAllowsOwnedContentHistoryAndNestedTask)$' -count=1
```

After the guard and original-task fixture updates:

```sh
GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/server -run 'TestSubagent' -count=1
GOMAXPROCS=4 go test -race ./cmds/delidev-cli/internal/worker -run '^TestSubagentClaude' -count=1
```

Both passed (server 19.400 s; Worker 10.768 s). `git diff --check` passed.
Administrator and ach embeds and the typed client were explicitly built; the
required desktop LFS asset preparation passed before validation. No schema,
migration, generated source, protocol, dependency, child controls or cleanup
relaxation was introduced. The owning server instructions, project invariant and
subagent contract record this boundary. Fixtures establish local publication and
receipt behavior only; full validation, new-head CI and native/account/platform
acceptance are separate evidence. Historical evidence remains unchanged.
