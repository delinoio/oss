# PR #1224: pre-native workspace failure runtime rollback

Follow-up inspection of the unused-runtime finding in
`PRRT_kwDORRAKg86ng0Y1` found the same retained directory on later workspace
validation/copy failures. Handling only the source-history rejection was
insufficient for those independent return paths.

One deferred guard now covers every return after successful runtime preparation.
Its closed phase tracks a proven-unused runtime, an unjoined source inspection,
or possible child-native state. A confirmed pre-native rejection removes the
unused runtime through the same synchronized absence proof; owned workspace
rollback stays independently owned by `PrepareFork`. Removal authority stops
before attempting the native child's process. Unjoined inspection, unknown
cleanup and possible native-child state remain uncertain and retained.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/worker -run
'^TestSessionFork' -count=1 -timeout=3m` passed in 1.938 seconds. Temporary
filesystem tests prove workspace rejection removes the runtime while retaining
its Conflict classification, unjoined inspection retains state as RecoveryRequired,
and possible native-child state is preserved. Existing source-history rejection,
failed removal and child-deletion ownership checks also pass. Logs contain only
the original job identity, closed runtime phase and stable failure code.
