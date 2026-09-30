# Issue #1079 fixed-source race result

## Source and command

The source-only archive of `46452512fcf4cfa832086419dfda45fba7b9f98f`
finished on 2026-09-30. It contains the root Go module, DeliDev command source and
generated protocol bindings, with LFS smudging disabled during archive creation.
No archive source changed while the command ran. From that archive's module root:

```sh
GOMAXPROCS=2 go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...
```

The command completed with exit status 1. The earlier partial records in
`current-main-workspace-storage.md` and `pr-1231-main-reconciliation.md` remain
historical observations; this record supplies the final result without rewriting
their source or failure distinctions.

## Complete result

Workspace passed (1,551.369s), as did server (1,480.257s), Worker (289.410s),
store (415.990s), domain, security, connections, credentials, forwarding,
process, providers, user services, API proxy, presentation, GitHub integration,
the shared harness and Claude, Codex, OpenCode and native-wire harness packages.
The command main and RPC packages reported no test files.

Two packages failed:

- CLI: `TestCLISessionAcceptanceQueueAndArchive` at `sessions_test.go:239`
  reported an unavailable creation-diff reader; package time was 502.193s.
  The same isolated test previously reproduced this exact failure on the branch
  and main `7090de04621ece95b3c2cce8d88fcfcdeadad7cc`, as recorded in the earlier
  evidence. That baseline comparison establishes only this specific failure on
  this host.
- Grok: `TestQuestionControllerOriginalClaimsAndUncertainty/question-claim-failure`
  at `question_reply_test.go:105` reported unavailable native initialization;
  package time was 1,242.225s. Its source matches the inspected main accounting
  revision, but this failure has no independently executed baseline comparison.

No full-suite success is claimed. The passing workspace package does not erase
the preceding broader fifteen-minute storage timeout, earlier faithful-restore
failure or disk-full setup failures. Their isolated passing reruns and unresolved
causes remain recorded separately.

## Scope limits

This source predates the forwarding ownership exclusion, main reconciliation with
`d1f83cecee4e0c50ea094335392cf68845f85739`, and the subsequent PR #1231 review
repairs. It cannot validate those later changes. Fixtures use isolated temporary
state, repositories and controlled native children. No real account, credential,
remote push, release publication or native Windows/Linux acceptance is established.
