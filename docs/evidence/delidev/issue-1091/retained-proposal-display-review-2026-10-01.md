# Retained original Grok proposal display review

Inspected base: `642f0161da82af077def33b526414342167733f5`.
Review: https://github.com/delinoio/oss/pull/1230#discussion_r4148427284.

## Finding and change

The server publishes original request bytes in `GrokToolEvent.proposal_json`.
The desktop event-key allowlist omitted that field, so production Write,
question and Plan requests and their request-type transcript observations were
unavailable. The display check now accepts this bounded inert string on request
observations, checks nonempty/NUL-free/well-formed UTF-8 text before complete
512 KiB event encoding, rejects notification placement and unrelated keys, and
preserves historical records that omit it. No parser, digest or native response
authority moves into the frontend; Go retains independent original validation.

The regression fixtures retain whitespace and original script-like content in
raw proposal bytes. They verify all three request families, their response
controls and transcript observations, inert rendering and unchanged raw bytes.
Negative cases cover null, object, empty, NUL, unpaired surrogate, excessive
UTF-8 bytes, unrelated keys and notification placement. Existing legacy,
numeric-identity, question bounds, native response and Plan focus cases remain.

## Executed verification

- `pnpm --filter @delinoio/delidev-api-client build`: passed.
- Before the display fix, the expanded interaction file reported three new
  positive-case failures and 25 passing tests. The Write fixture's offered IDs
  were then aligned with the existing pinned native enum before final testing.
- `pnpm exec vitest run src/native-grok-interactions.test.tsx
  src/native-grok.test.tsx src/inbox-drafts.test.tsx src/views.test.tsx
  --maxWorkers=1`: passed; two matching files, 119 tests, 5.48 seconds.
- `pnpm typecheck`: passed.

The required complete frontend script is recorded separately after the repair
commits. These component checks do not prove hosted-account/native platform
acceptance, filesystem confinement or a complete passing repository suite.
The existing automatic outside-workspace Read policy decision remains pending.
