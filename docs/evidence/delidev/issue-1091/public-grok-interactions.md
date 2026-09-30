# Public Grok original tools, questions and Plan

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). Inspected base: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. Pinned native profile: Grok Build `1.0.41`.

## Implementation boundary

Original typed Read/Write/question/Plan events now share durable public Worker/server Resource publication, authenticated response claims, session/Inbox controls and existing CLI response documents. Native Read has no client permission request in the pinned profile; its automatic permission cycle is retained without invented denial. Write once/session/rejection and native question/Plan outcomes retain their original semantics. Original Plan revision/entry/Write/content ownership is preserved; no common synthetic Plan gate is introduced. Native response transport, original result acceptance, root terminal and process/workspace cleanup remain distinct. Lost publication acknowledgments repeat only exact receipts, never native replies.

Existing first-text successful history and original Stop remain separately tested. Repositories, continuation, unsupported tools, richer native Stop proof, real account/platform/release acceptance and the full issue #964 completion boundary are not established by this change. All ordinary regressions use temporary private state and controlled fixture processes without user credentials, provider inference or external publication.

## Executed validation

- Root frozen `pnpm install` and exact-path Git LFS hydration of the desktop icon succeeded before asset validation.
- API client build and desktop TypeScript typecheck passed.
- `pnpm proto:check` passed lint, breaking and reproducible generated bindings.
- Original public projection tests passed for mixed tools and approved/cancelled/abandoned/revised Plan fixtures, preserving original exact JSON values.
- Transactional public Write tests passed for once/session/rejection, exact receipt replay, competing responses, wrong response kind, stale revisions, Stop and Worker-instance revocation.
- Controlled original first-input process tests passed for Write rejection and native Plan completion; the initial broader run also passed Write allow and existing first text. A loaded-host initialization deadline failure was rerun successfully.
- Focused new desktop tests passed (9 tests), including exact large revision, cancellation, original answer/notes, revision display and malformed/mixed request rejection.
- Required default `pnpm test` was executed twice. The cancellation-form defect found in the first run was corrected and focused tests passed. The second run had 80 passing files/961 passing tests and 5 failing files/8 failing tests with existing fixture/timeouts under concurrent host load. A serial suite is in progress to distinguish contention from regressions.
- Required root Go race suite is in progress with bounded package concurrency; an existing CLI workspace fixture exceeded its operation deadline. Remaining outcomes and focused response/Plan regressions will be recorded after completion.

No unperformed native/account/platform acceptance is inferred from parser, transactional or controlled-process results.
