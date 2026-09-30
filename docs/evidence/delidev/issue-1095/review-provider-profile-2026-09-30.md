# Issue #1095: accepted Codex provider profile in publication and checkpoints

While evaluating PR #1233's terminal execution feedback, source inspection found
two additional API-only assumptions: thread publication and checkpoint matching
required the relay provider even for accepted managed subscription execution.

On revision `0a491fcfa`, controlled durable-outbox and private-checkpoint tests
reproduced both rejection of built-in OpenAI and acceptance of the wrong relay
provider for a subscription configuration. Four subscription cases failed;
the corresponding API profile controls passed.

Native settings, thread publication and checkpoint retention/read now select one
exact provider from the immutable accepted authentication profile. The private
checkpoint reference carries an expected subscription flag copied from that
configuration. Tests also reject reads using the opposite comparison profile.
The serialized API checkpoint model and bytes remain unchanged; independent
uncertain-subscription recovery is not widened.

`GOMAXPROCS=4 go test -race -p 1 -timeout=10m
./cmds/delidev-cli/internal/worker
-run 'Codex.*ProviderMatches|ExecutionCheckpoint|CompletedExecution' -count=1`
passed after the fix (7.133-second package), including retained checkpoint and
completed-execution inspection controls. This is focused fixture evidence, not
an installed-native or real-account subscription execution claim. Full validation
must use the final implementation after the separate review repairs.
