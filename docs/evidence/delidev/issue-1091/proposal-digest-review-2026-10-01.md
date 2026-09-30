# Original Grok proposal digest review, 2026-10-01

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). This repair addresses
[the proposal-digest finding](https://github.com/delinoio/oss/pull/1230#discussion_r4146018122)
reported on `da889167efdabc2734167e094caca853e912e74d`. The date uses Asia/Seoul;
the initiating heartbeat was `2026-09-30T14:55:40.466Z`.

The finding is supported: the server checked the request-ID digest and original
Plan metadata but did not independently compare the private proposal digest.
That digest is SHA-256 of the original native JSON parameter bytes. Serializing
the typed projection cannot recover JSON member order, whitespace or escape
spelling and would reject valid original requests.

New request observations retain those original bytes as `proposal_json` beside
the closed typed payload. The pure reducer requires their decoded payload to
match, applies the original native validators to those bytes and retains their
SHA-256 under the original arrival. Interaction validation compares that digest
before persistence. Notifications cannot carry request byte evidence, and both
representations remain inside the existing event/journal bounds. No native claim
digest, response encoding, filesystem permission, protobuf number or migration
is changed. Historical typed-only records remain readable but cannot acquire a
new interaction/reply through reconstructed byte identities.

The regression exercises original Write, question and Plan requests with valid
non-canonical JSON formatting, exact retention through Resource JSON, altered
digests and foreign-byte/typed-payload mismatch. SQLite publication fixtures
require changed digests to create no interaction and leave session progress
unchanged, then accept the original request and its normal reply/receipt paths.
A historical typed-only interaction cannot acquire a new approval reply.

Validation uses private test resources without user credentials or inference.
The task-private `GOCACHE=/tmp/oss-1091-go-cache` and `GOMAXPROCS=4` are shared by
Go commands. An initial race run passed Grok harness (6.999 seconds), server
(47.082 seconds) and Worker (2.104 seconds) selections. After adding the
historical-reply guard, the final race selection passed domain (1.747 seconds),
Grok harness (25.980 seconds), server (49.985 seconds), Worker (4.369 seconds)
and CLI (2.736 seconds). Its exact root command was:

```sh
go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/grok ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/cli -run 'Test(PublicProposal|PublicToolJournal|PublicNumericRequest|GrokPublic|GrokInitialPlanTerminal|GrokExtraReceipt|GrokResponses|GrokExactNative|GrokDecimal|CLIGrok)'
```

Required DevHud administrator and async-commit-hook embedded builds passed before
CLI compilation. Generated output is untracked and removed after final hooks.

The separate exact request-ID review is a distinct repair. The existing
[outside-workspace automatic Read finding](https://github.com/delinoio/oss/pull/1230#discussion_r4144405319)
still awaits the previously requested policy decision. This digest repair does
not establish filesystem confinement or review/merge approval. Required final
integrated validation and new-head CI/reviews remain separate evidence.
