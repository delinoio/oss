# Issue #1095: final bundle capture before native Close

[PR #1233 feedback](https://github.com/delinoio/oss/pull/1233#discussion_r4144990742)
correctly identified a terminal ordering defect. After the separate provider
profile fix at `ac06334d7`, a controlled native-process execution fixture reached
acknowledged thread/input/message/terminal publication and retained its checkpoint,
then failed with incomplete managed authentication because the deferred bundle
read used the already-closed native wire.

The execution owner now captures native identity and the final bundle once before
terminal Close. Earlier exits use the same bounded read before their deferred
Close, without retry. Joined native cleanup precedes the file comparison/removal,
retained-history scan and protected write-back.

The fixture rotates its synthetic auth file during the turn, verifies successful
original completion, changed token/refresh evidence, one native bundle read,
confirmed protected write-back and absence of both original and rotated tokens
from retained Worker files. Its case budget is one minute; the production bundle
read retains its existing five-second bound. No installed CLI or provider account
is involved.

The first broader fixture run failed because the new native test driver also
intercepted separately tagged login lifecycle drivers. The test routing was
corrected to recognize direct native arguments only; this was a fixture defect,
not evidence of an additional native product failure.

Final `GOMAXPROCS=4 go test -race -p 1 -timeout=10m
./cmds/delidev-cli/internal/worker
-run 'Managed|ExecutionCheckpoint|Codex.*ProviderMatches' -count=1` passed
(169.495-second package), including existing login, rotation, logout, cancellation,
lost-write-back, cleanup and checkpoint controls. Full validation remains scoped
to the eventual final implementation; real-account/platform acceptance is separate.
