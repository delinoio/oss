# DeliDev session acceptance and input queue contract

## Scope
`cmds/delidev-cli/internal/domain/session.go`, `internal/store/sessions.go`, `internal/server/sessions.go` and `internal/cli/sessions.go` own durable session acceptance, ordered input, revision-checked edits, visibility and inactive controls. Authenticated `SessionService` is implemented end to end through the CLI/server and real SQLite.

Native session dispatch, first-execution snapshots/routing, workspace preparation jobs, live Stop/Archive cleanup, Resume, Steer, Plan execution, interactions, native recovery and forks remain pending. Creation currently returns `outcome=not-started`, `dispatch=blocked`, and an explicit unsupported execution problem. It does not create a native thread, workspace, proxy credential or inference request. Never treat accepted queued input as a successful harness execution.

## Runtime and Language
Go with the root dependencies, existing Connect transport and private SQLite authority. No client-owned database or in-memory-only acceptance queue.

## Users and Operators
Owner and paired clients may accept sessions and manage their inputs. Worker credentials cannot call these owner operations. Each session keeps its selected execution machine independently of whichever client later views it. Creating-client provenance is server-derived from authentication; source labels distinguish manual and external CLI intent without granting authority.

## Interfaces and Contracts
`session create --input FILE|-` accepts the closed `domain.CreateSession` document. Every path selects exactly one Agent Worker, never separate harness/account/model overrides. Project sessions default to Worktree; projectless creation requires explicit `general-chat`. Initial mode defaults to `execute`; `plan` records intended native mode without claiming support. RPC creation defaults to `MANUAL`, while the CLI always supplies `EXTERNAL_CLI`.

```json
{
  "name": "Inspect the parser",
  "agent_id": "<canonical Agent Worker UUID-v7>",
  "machine_id": "<canonical execution Worker UUID-v7>",
  "project_id": "<canonical project UUID-v7>",
  "workspace": "worktree",
  "starting": [],
  "prompt": "Inspect the parser and explain its current behavior.",
  "mode": "execute"
}
```

Each optional starting entry has `repository_id` and a typed `reference`. Selections must belong to the project's repositories; every repository needs a configured checkout on the chosen enabled machine. General Chat cannot select a project or Git references. Local cannot select starting references and currently returns explicit unsupported: authenticated client-to-local-Worker origin verification is not implemented, and caller-supplied IDs cannot substitute for it. No remote checkout is silently reinterpreted as Local.

Creation validates the selected Agent Worker's current relationships and the project's Agent restriction, then atomically creates the session and first input. It retains selections only; it does not freeze Agent Worker settings/templates, choose an account or advance routing. Account-less Agent drafts may be retained as blocked sessions. Future dispatch must recheck all current project/account/machine/native restrictions, resolve all effective settings and exact instructions, and commit the immutable snapshot and account routing state together immediately before execution. Later configuration changes must not rewrite an established execution snapshot.

`session get --id ID` reads current metadata. `session list` supports `--project-id`, `--include-archived`, `--limit` and `--page-token`; ordinary lists hide only fully archived sessions. Generic explicit resource reads/snapshots can still retrieve retained archived records. Lists use canonical identity order and signed filter-bound cursors, with at most 200 records and 3 MiB of stored document bodies per page.

`session enqueue --id ID --input FILE|-` accepts `{"prompt":"...","mode":"execute"}`. Its transaction increments a session-owned acceptance sequence and retains the exact intended mode, content revision and `queued` delivery state. Concurrent requests use this durable order, never client clocks or UUID generation order. Queues are capped at 1,000 pending inputs and 4 MiB of pending prompt text; one input is at most 256 KiB of well-formed NUL-free UTF-8. Accepted blocked/paused sessions retain follow-ups; archiving/archived sessions reject new input. Queueing never supplies an approval or resumes work.

`queue list --session-id ID` returns acceptance order with byte/record-bounded pages and session-bound sequence cursors. `queue edit --session-id ID --id ID --revision N --input FILE|-` accepts only a `prompt` field, preserving accepted mode and sequence while incrementing content and resource revisions. `queue remove` (also `queue delete`) requires the same identities/revision. Only `queued` input can change; claimed, accepted, uncertain or removed input fails. Removal replaces content with an empty durable `removed` tombstone, updates pending counts/bytes, and excludes the entry from queue lists. It does not reuse its identity/order or resurrect content on receipt replay.

`session stop|archive|restore|resume --id ID --revision N` uses closed typed controls. In this increment Stop and Archive can complete only for never-executed sessions with no active execution/recovery/cleanup ownership. Stop pauses dispatch while retaining `not-started`; Archive additionally marks retained visibility archived. Restore (also `unarchive`) clears visibility only and keeps dispatch paused. Queue contents and outcome remain unchanged. Resume returns explicit unsupported until native execution integration exists. Records with running/previous execution, uncertain recovery or archive cleanup cannot be declared stopped by a metadata-only operation. Future live controls must durably pause first, revoke execution authority and confirm owned native cleanup before reporting success; Stop and Archive have different terminal/process/forward ownership boundaries in the normative issue.

`session rename --id ID --revision N --name NAME` changes only the bounded title. Creation, enqueue, edits/removal, rename and controls use UUID-v7 request receipts and the same server serialization boundary. Mutations requiring an existing revision reject stale writes; retries of an accepted identical request return current referenced records. An old creation/enqueue/edit receipt cannot restore earlier content or repeat dispatch. Unsupported or pre-acceptance validation failures do not claim a successful receipt. No future schedule/remediation/fork source is accepted through this initial external creation schema.

## Storage
Schema v4 adds indexes for session visibility, unique per-session queue sequence and pending delivery order. The migration preserves a synchronized, validated pre-migration backup from v1/v2/v3 and rolls back conflicting legacy queue identities without rewriting accepted order. Session/input records, counts, metadata-only events and request receipt commit in one transaction. No Git, harness, credential or filesystem work runs inside it.

Receipts contain session/input identities rather than copies of prompt bodies. Original content removed through the queue API is absent from current records and receipt responses; physical backup/WAL retention remains subject to the pending coordinated deletion/backup contract. Session snapshots and queue reads remain available after restart. Archive never deletes workspace or transcript resources. Referenced Project/Agent configuration is currently protected from deletion until the full session retention/deletion lifecycle is implemented; unrelated configurations are not blocked by a session's presence.

## Security
The existing authenticated Connect boundary rejects Worker calls and revoked client credentials, including mutation replay. Store mutations revalidate the current principal inside the transaction. Client documents cannot set outcome, recovery, archive, selected account, active execution identity, sequence or delivery state. General configuration writes/deletes cannot bypass the dedicated lifecycle API. Queue edit/remove checks both owning session and exact current input revision. Native acceptance uncertainty is preserved and cannot become editable queued content.

The upcoming execution owner must serialize native claims, Steer, interaction replies, Stop and Archive with this same durable session state. Never infer exactly-once native delivery from exactly-once local request acceptance, or allow proxy authority from a blocked/paused/archived/recovery-pending session. Local origin proof, native capabilities and complete snapshot/account checks remain requirements, not waived by the current metadata implementation.

## Logging
Structured session acceptance, input acceptance/change and control logs contain correlation/request/session/input/machine identifiers, closed source/action, removal selection and replay status. Never log names, prompts, raw documents, native input/output or credentials. Existing RPC failures retain safe typed classification and recovery guidance.

## Build and Test
- `go test -race ./cmds/delidev-cli/...`
- `go vet ./cmds/delidev-cli/...`
- Root Buf formatting/lint and reproducible generation after schema changes.

Real temporary SQLite/Connect tests cover creation/restart/replay, immutable input mode/order, concurrent enqueue, scoped pagination, edits and tombstones, revision conflicts, Worker denial, blocked native execution, independent visibility/outcome/pause, uncertainty refusal, and no partial acceptance on invalid selections. CLI tests exercise pairing, configuration, create/queue/edit/remove/archive/restore and the truthful unsupported Resume response. These do not prove live native session execution. Migration tests retain v3 backups and fail closed on duplicate queue order; large pages prove aggregate byte bounds.

## Dependencies and Integrations
Uses `ResourceService` for individual reads/snapshots/events and the existing configuration/account/Worker registries. Native dispatch will integrate the workspace, owned process, harness and API relay boundaries; it must not weaken their independent ownership contracts.

## Change Triggers
Update this document, project index, protocol contract, CLI contract, evidence ledger and scoped AGENTS whenever acceptance, lifecycle, origin proof, delivery/retry, migration or execution integration changes. Preserve the complete requirements and distinguish fixtures from actual native evidence.

## References
- [Project](project-delidev.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Connect contract](protos-delidev-v1-contract.md)
- [Worker workspace contract](cmds-delidev-workspace-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Repository defaults](repository-defaults.md)
