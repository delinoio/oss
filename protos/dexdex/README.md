# DexDex Proto Contracts

`protos/dexdex/v1/*.proto` is the shared source of truth for DexDex Connect RPC contracts.

## Multi-File Layout

- `common.proto`: shared enums, cursor pagination, mutation metadata, commit metadata.
- `error_details.proto`: typed `google.rpc.Status` detail payload contracts.
- `workspace.proto`: `WorkspaceService`.
- `repository.proto`: `RepositoryService`.
- `task.proto`: `TaskService`.
- `session.proto`: `SessionService`.
- `pr_management.proto`: `PrManagementService`.
- `review_assist.proto`: `ReviewAssistService`.
- `review_comment.proto`: `ReviewCommentService`.
- `badge_theme.proto`: `BadgeThemeService`.
- `notification.proto`: `NotificationService`.
- `event_stream.proto`: `EventStreamService`.

## Breaking Changes

- Removed legacy `protos/dexdex/v1/dexdex.proto` single-file contract.
- All list APIs now use cursor pagination (`page_size`, `page_token`, `next_page_token`).
- All mutation APIs now include `request_id` and `idempotency_key`.
- `SessionOutputEvent.body` was replaced by `kind + oneof payload`.
- `SubmitPlanDecisionResponse` is now outcome-typed (`approved|revised|rejected`).
- `WorkspaceEventEnvelope` now exposes one payload entry per `StreamEventType`.
- `StreamWorkspaceEvents` now returns `StreamWorkspaceEventsResponse` with an `event` envelope.

## Validation

```bash
cd protos/dexdex
buf lint
buf build
```

## Go Artifact Generation

```bash
cd protos/dexdex
buf generate
```

Generated Go artifacts are emitted under `protos/dexdex/gen` and are reproducible outputs.

## Error Model

RPCs use Connect/gRPC status codes with typed details from `error_details.proto`.

- `INVALID_ARGUMENT` -> `ValidationErrorDetail`
- `FAILED_PRECONDITION` -> `StateMismatchDetail`
- `OUT_OF_RANGE` -> `EventStreamCursorOutOfRangeDetail`
- `PERMISSION_DENIED` -> `AuthorizationDeniedDetail`
- `NOT_FOUND` -> `ResourceNotFoundDetail`

## Example Payloads

### `SubmitPlanDecisionResponse` (`revised`)

```json
{
  "revised": {
    "updatedSubTask": {
      "subTaskId": "sub-101",
      "status": "SUB_TASK_STATUS_COMPLETED",
      "completionReason": "SUB_TASK_COMPLETION_REASON_REVISED"
    },
    "createdSubTask": {
      "subTaskId": "sub-102",
      "type": "SUB_TASK_TYPE_REQUEST_CHANGES",
      "status": "SUB_TASK_STATUS_QUEUED"
    },
    "revisionNote": "Please improve failure-case test coverage."
  }
}
```

### `SessionOutputEvent` (`tool_result_payload`)

```json
{
  "sessionOutputId": "out-22",
  "sessionId": "sess-7",
  "kind": "SESSION_OUTPUT_KIND_TOOL_RESULT",
  "occurredAt": "2026-03-04T05:20:00Z",
  "toolResultPayload": {
    "toolName": "cargo test",
    "resultJson": "{\"status\":\"ok\",\"passed\":128}",
    "isError": false
  }
}
```

### `StreamWorkspaceEventsResponse` (`SUBTASK_UPDATED`)

```json
{
  "event": {
    "sequence": "142",
    "workspaceId": "ws-1",
    "eventType": "STREAM_EVENT_TYPE_SUBTASK_UPDATED",
    "occurredAt": "2026-03-04T05:20:30Z",
    "subTaskUpdated": {
      "subTaskId": "sub-102",
      "unitTaskId": "unit-15",
      "status": "SUB_TASK_STATUS_IN_PROGRESS"
    }
  }
}
```

## Migration Strategy

1. Replace imports of `dexdex/v1/dexdex.proto` with the domain file imports needed by each server/client package.
2. Update list RPC callers to send/consume cursor pagination objects.
3. Update mutation RPC callers to populate `mutation.request_id` and `mutation.idempotency_key`.
4. Update `SessionOutputEvent` consumers from `body` string parsing to typed payload `oneof`.
5. Update event stream consumers to handle the expanded `WorkspaceEventEnvelope` payload variants.
6. Re-run:
   - `cd protos/dexdex && buf lint`
   - `cd protos/dexdex && buf build`
   - `cd protos/dexdex && buf generate`
