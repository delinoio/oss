# Exact Grok request identity review, 2026-10-01

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). This distinct repair addresses
[the request-ID representation finding](https://github.com/delinoio/oss/pull/1230#discussion_r4146018133)
reported on `da889167efdabc2734167e094caca853e912e74d`, after the separate proposal
digest repair. The date uses Asia/Seoul; the initiating heartbeat was
`2026-09-30T14:55:40.466Z`.

The finding is supported: legacy signed-number and Grok decimal identities for
an ordinary integer share the same `n:` duplicate-detection key. Comparing only
that key admitted an outer legacy number for a retained decimal request. Their
public kind/value representations then differed and desktop exact comparison
could not expose the original reply controls.

Grok interaction validation now checks both IDs' valid shapes and compares their
complete values, including kind, decimal spelling and numeric value rather than
pointer address. Namespace keys and native request digests remain unchanged for
duplicate detection and receipt compatibility. No protobuf allocation, schema
migration, native callback, response family or filesystem permission is changed.

The domain regression uses an original native Write request and covers matching
text, separately allocated equal legacy numbers, decimal integers, `-0` and an
integer outside int64. It rejects both directions of number/decimal substitution,
changed decimal spelling and changed namespace while retaining equal duplicate
keys for the legacy/decimal pair. SQLite publication fixtures reject an outer
number for original decimal `0`, `42` and `-42` without creating an interaction
or advancing session progress, then accept the original representation and its
normal approval, owning-Worker claim and receipt replay. Larger lexical IDs and
negative zero retain their original existing positive paths.

Focused race validation uses private temporary resources, no user credentials
or inference, `GOCACHE=/tmp/oss-1091-go-cache`, `GOMAXPROCS=4` and this root command:

```sh
go test -race -p 2 -timeout=20m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server -run 'Test(GrokInteractionPreservesExact|GrokDecimal|GrokPublicNumericApproval)'
```

The domain selection passed in 1.459 seconds and the server selection passed in
4.734 seconds. Required final integrated validation and new-head CI/reviews
remain separate evidence. The earlier broad
validation failures remain preserved in their original records. The separate
automatic outside-workspace Read policy decision remains pending; this repair
does not establish confinement, risk acceptance or merge approval.
