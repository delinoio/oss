# Original actor ownership for compaction receipts

At implementation commit `8817d892`, the new authenticated
`TestCompactionReceiptIsBoundToOriginalActor` reproduced foreign receipt replay:
a separately paired client reused the owner's exact accepted request and received
success. The test failed with that observation (13.868 s package run).

The repair includes the original non-secret principal in the mutation digest.
It preserves reference-only original replay and authorization-before-replay,
rejects another authorized client's request identity and rejects revoked clients
before receipt lookup. No receipt table or historical record is rewritten.

The regression also checks original job/revision preservation and revoked-client
rejection. After repair, `go test -race -p 1
./cmds/delidev-cli/internal/server -run
'Test(CompactionReceiptIsBoundToOriginalActor|PublicCompactionAtomicReceiptAndFIFO|CompactionSessionDeletionRetainsOriginalActionOwnership)$'
-count=1` passed (120.050 s). Full `go vet ./cmds/delidev-cli/...` passed again.
These controlled authenticated checks do not establish installed-native acceptance.
