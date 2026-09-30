# Issue #1095: committed Finish presentation cancellation

Review thread `PRRT_kwDORRAKg86nt1O9` identified recovery publication after the
ownership mutation and final vault cleanup had already settled. A canceled
response-resource read incorrectly changed the committed account to failed and
recovery-required; its accepted receipt could not undo that extra fence.

Finish now ends its ownership-error guard after the last required vault cleanup,
before reading the response resource. A structured presentation-failure event
contains only original opaque identities and the stable failure code. Errors
before that barrier, including failed final vault enumeration, still fence
ownership. Receipt replay remains read-only and preserves either settled state
or an independently required recovery fence.

SQLite/temporary-vault fixtures cover a cleanup-confirmed failed login and a
confirmed unused-original execution. Both verify settlement from the final
vault observation, then cancel the request before presentation. They check
generation/connection/health preservation and exact receipt replay with no
additional vault work. Separate negative cases fail that final vault observation
and require the recovery fence to remain, including through replay.

Executed on macOS arm64 with synthetic credentials:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/server -run '^TestSubscriptionCommittedFinish' -count 1
# Unfixed source: both presentation-cancellation cases failed, exit 1;
# final-cleanup-failure controls passed.
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/server \
  -run 'SubscriptionCommittedFinish|SubscriptionLostOwnership|SubscriptionAcceptedFinish|SubscriptionConfirmedPreNative|SubscriptionPreNativeRelease' -count 1
# Fixed source: passed, 20.419s.
```

This does not establish real-account, installed-native or Windows acceptance.
Required complete validation and the outstanding Windows Workspace CI failure
are recorded separately; previous failures remain preserved.
