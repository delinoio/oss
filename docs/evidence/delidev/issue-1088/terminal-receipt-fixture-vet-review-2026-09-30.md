# Terminal receipt fixture vet repair

The first final `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` run
reported four protobuf lock-copy warnings in the new receipt-recovery fixture.
`ReportTerminalRequest` contains protobuf message state with a mutex; copying its
struct is unsafe even when the fixture only intends to vary a request field.

The fixture now uses `proto.Clone` before each changed-input assertion, preserving
independent message state and the original receipt request. Production behavior,
authority checks and assertion/deadline requirements are unchanged.

Repaired-source validation, 2026-09-30 UTC:

- Root `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run
  'TestTerminalReplacementReadsOnlyCommittedOriginalReportReceipt' -count=1`:
  passed, 22.701s.

The initial vet failure is retained in this record; the pass above applies after
safe protobuf cloning. Broader validation is recorded separately for this pass.
