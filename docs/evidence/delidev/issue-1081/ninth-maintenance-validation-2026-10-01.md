# Ninth manual PR-fix maintenance validation

## Stable revision and commands

The complete command-domain attempt ran against merge revision
`2eadf57e7fd4146569d2dcc8c14d58f26cc96dda`, combining prior pushed head
`b787b03ea252ac7ce12a92df34af70516f035a88` and main
`ef5dbf8974ee69fb262cf66fc174f91e97a0e86f`. No source mutation occurred during
this attempt. The following evidence commit changes only this record.
See [merge/restoration evidence](ninth-maintenance-main-ef5dbf89-2026-10-01.md)
for the conflict decisions, focused tests, credential/publication limits and
externally resolved companion-data review thread.

```sh
go test -race -count=1 -timeout=20m -json ./cmds/delidev-cli/...
```

The retained checkout `.gomodcache` was selected explicitly through `GOMODCACHE`.
The command exited 0 in **971.265 measured seconds**. All **23 tested packages
passed**; three package entries had no test files. JSON events retain **8,676
passing test/subtest results, 427 skipped results, no failed result and no
unfinished test**. These counts include parent/subtest events, not independent
acceptance scenarios. No package watchdog expired.

| Package | Result | Package-reported seconds |
| --- | --- | ---: |
| delidev-cli | skip | 0 |
| apiproxy | pass | 1.874 |
| cli | pass | 229.119 |
| connections | pass | 18.983 |
| credentials | pass | 5.849 |
| domain | pass | 4.264 |
| forwarding | pass | 3.702 |
| harness | pass | 17.685 |
| harness/claude | pass | 120.65 |
| harness/codex | pass | 119.184 |
| harness/grok | pass | 967.406 |
| harness/nativewire | pass | 12.954 |
| harness/opencode | pass | 53.335 |
| integrations/github | pass | 14.675 |
| outbound | pass | 5.307 |
| outboundtest | skip | 0 |
| presentation | pass | 6.982 |
| process | pass | 16.885 |
| providers | pass | 8.615 |
| rpc | skip | 0 |
| security | pass | 4.906 |
| server | pass | 853.653 |
| store | pass | 347.427 |
| userservice | pass | 8.576 |
| worker | pass | 227.532 |
| workspace | pass | 858.56 |

Skipped results include explicit private native harness/credential fixtures and
public GitHub capture fixtures. For example, native Secret Service requires a
disposable credential session; manual Claude/Grok discovery requires explicit
private validation; public CI/rules/reviewer projection tests require supplied
capture fixtures. This ordinary test invocation does not establish those
opt-in outcomes or use real user accounts/inference. Every skip remains a skip.
The complete workspace package passed in 858.560 seconds in this run; its older
failed/unfinished attempts remain recorded without being rewritten or dismissed.

## Other completed checks

- Root `pnpm proto:check` passed after the merge commit: schema formatting/lint,
  main-relative breaking compatibility and forced generation freshness all
  succeeded. The forced generation task reported 1.384 seconds and no generated
  binding drift. Both service-specific and compatibility exports are retained.
- DeliDev API-client lint, all 47 tests in six files and build passed before the
  merge commit on the identical reconciled source; Vitest reported 21.37 seconds.
- The eight-package focused race selection passed in 80.617 measured seconds.
- `go vet ./cmds/delidev-cli/...` passed in 2.414 measured seconds; the CLI build
  passed in 0.837 seconds, writing its executable under `/tmp` only.
- Required administrator and async-commit-hook embed builds passed. Root frozen
  pnpm installation and tracked LFS hydration were completed before compilation.
- Tools observed after validation: Go `1.26.8` on `darwin/arm64`, Node `24.20.0`
  and pnpm `10.26.2`. These are local observations, not other-platform evidence.

No app frontend or Rust source changed in this merge, and no complete app frontend,
root repository Go or root Cargo test was run in this pass. The generated API
client checks and backend command-tree pass do not imply those broader results.
All earlier independent validation records, including failed frontend/root Go
attempts, remain unchanged. No native/account/platform/release or immutable
Git/publication security acceptance is claimed.

## Push and review boundary

This evidence is local pre-push validation of the exact stable merge source.
The final evidence commit contains no implementation change. The one final push
and resulting PR inventory are verified by maintenance separately; no CI or
review result for that future pushed head is inferred from the previous-head
successful run. The original Git execution/configuration P1 is not repaired by
these tests. The externally resolved companion thread is not proof of an
implemented reviewed-publication boundary, and no pending human profile decision
was answered by the heartbeat.
