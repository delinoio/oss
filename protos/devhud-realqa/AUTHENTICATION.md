# RealQA authentication metadata and scopes

Credentials never appear in `devhud.realqa.v1` messages. Human RPCs carry both
short-lived credentials in Connect HTTP metadata:

```text
Authorization: Bearer <realqa-audience-user-access-token>
x-delibase-forwarded-user-token: <delibase-audience-user-access-token>
```

The RealQA token targets the future `https://realqa.deli.dev` audience. The
forwarded token targets `https://delibase.deli.dev`. RealQA verifies that both
tokens have the same Logto subject and the required scopes before business
authorization. Both values are memory-only credential material and must be
redacted from logs, traces, errors, caches, persistence, idempotency digests,
and diagnostics.

| RPC group | Required RealQA user scope |
| --- | --- |
| Preset reads | `realqa:presets:read` |
| Preset writes and owner-request feature deletion | `realqa:presets:write` |
| Tracker reads | `realqa:tracker:read` |
| Tracker connection mutations | `realqa:tracker:write` |
| Submission reads | `realqa:submissions:read` |
| Submission, upload, submit, rebind, and deletion mutations | `realqa:submissions:write` |

The implemented foundation requires `delibase:account:read` on the forwarded
bearer for preset/tracker calls and submission reads. Submission mutations
require `delibase:usage:execute`, matching the eventual live usage boundary;
`SubmitIssue` additionally requires `delibase:billing:write` because it creates
the initial submission-bound storage authorization.
`RebindSubmissionStorageAuthorization` instead requires
`delibase:billing:read` and `delibase:billing:write`; GitHub connection
disconnection requires those two scopes plus `delibase:account:read` so it can
validate and revoke each exact submission-bound grant before discarding the
connection. The forwarded bearer is never sent to GitHub, R2, the recurring
M2M authorized-usage worker, or a public image handler, and the RealQA server
never retains it.

`RealQAPresetService.DeleteFeatureData` in a delibase lifecycle mode is the sole
exception. That mode uses only `Authorization: Bearer
<realqa-lifecycle-m2m-access-token>` with the exact RealQA-scoped delibase
service identity and lifecycle scope. It rejects the forwarded-user header.
Every other RealQA procedure rejects that lifecycle M2M identity.

Raw Logto client secrets, refresh tokens, GitHub tokens, R2 credentials, signed
upload headers, and webhook secrets are never accepted as protobuf fields.
