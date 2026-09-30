# Pending terminal input projection review

Source: PR #1226 review `PRRT_kwDORRAKg86nswzr`, inspected at
`964d2981e1e81f071707b01d204f0027d79a8d77`.

Accepted input bytes previously appeared in the Terminal pending operation's
public Resource JSON. A no-echo password could therefore appear in CLI control
output or another client's metadata reads before Worker completion. The common
RPC Resource projection now omits pending input bytes. It retains operation ID,
action, claim and pending state and leaves the private stored dispatch untouched.
Original authenticated Worker watch/claim assignments still carry exact bytes.
Malformed terminal documents never fall back to their private original bytes.

Validation on the repaired source, 2026-09-30 UTC:

- The new real isolated Connect server fixture failed the public control-response
  assertion before the repair.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/rpc
  ./cmds/delidev-cli/internal/server -run
  'TestTerminal(ResourceProjection|InputDispatch)' -count=1`: passed,
  RPC 2.733s and server 6.117s.
- Coverage includes control response/replay, generic inspect/list/snapshot,
  output-stream metadata, later claimed public metadata, exact private Worker
  watch/claim bytes, unchanged stored input, malformed documents and preservation
  of non-terminal resources.

This is controlled protocol/projection evidence. It does not establish real
account/remote Worker, native no-echo shell or desktop/release acceptance. Final
broad validation and remaining revocation-authority limits are recorded separately.
