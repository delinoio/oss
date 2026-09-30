# PR #1232 device resource privacy

Codex thread `PRRT_kwDORRAKg86nv4s8` identified that generic Device documents
bypassed BrowserService's device-scoped reads. Get/List/Snapshot and device
pairing/revocation responses now share a projection that omits `browser_profiles`
for every caller. Original stored bytes, revisions and unrelated fields remain
intact. Invalid device JSON fails with recovery-required instead of exposing a
raw document. Dedicated browser ownership and cleanup reads remain unchanged.

Validation on 2026-10-01:

- `GOMAXPROCS=2 go test -race -p 1
  ./cmds/delidev-cli/internal/server -run
  'TestBrowser|TestWorkerPairingOwnershipDispatchAndRevocation' -count=1`
  passed in 11.988 seconds.
- The new HTTP test covers owner and both paired clients, own/foreign Device
  Get reads, List and coherent Snapshot, binary and JSON Connect encodings,
  pending/removed inventories, stored-byte preservation, original dedicated
  browser reads, foreign-profile denial and revocation projection.
- A projection test preserves unknown unrelated fields and rejects malformed,
  null and non-object device documents. Existing pairing/retry/Worker role
  tests also pass.

Full required checks are recorded independently after the remaining review
evaluation. No real credentials or browser data were used.
