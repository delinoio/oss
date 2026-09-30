# Claimed cancellation and late reports

Codex thread `PRRT_kwDORRAKg86nfo4e` identified that a valid report arriving after
claimed cancellation could clear action ownership, install a successor checkpoint
and complete Archive. Source inspection confirmed that the cancellation flag only
guarded ready dispatch.

The repair checks cancellation before publishing settlement. Valid observed output
is retained on the uncertain job, while recovery, original action ownership, the
preceding checkpoint and pending Archive remain authoritative. The prior execution
outcome and queued input remain unchanged; old conversation recovery cannot release
the action. No report receipt or cancellation record is rewritten.

Authenticated regression scenarios exercise Stop, Archive and account disconnect
after claim/registration and then a valid report plus exact report replay. The first
post-repair run passed Stop and disconnect, but Archive failed at the preceding
fixture's assignment deadline before compaction (68.198 s package result). The
pre-repair attempt was interrupted during compilation and supplies no regression
outcome; the original defect is established by source inspection.

With reduced test concurrency, `GOMAXPROCS=2 go test -race -p 1
./cmds/delidev-cli/internal/server -run
'^TestPublicCompactionAtomicReceiptAndFIFO$/^claimed-' -count=1` passed all three
scenarios (46.097 s). Production deadlines and native acceptance bounds were not
changed. This is controlled public RPC evidence, not installed-native acceptance.
