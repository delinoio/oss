# Issue #1095: review repairs reconciled with newer main

This independent record follows the four review-repair records and preserves
all earlier runs. The previous local source was
`19783561418365a3f55f324a642dcb8eeeb69728`; the fetched main revision is
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`.

The first final protocol check found that shared `origin/main` had advanced with
the stopped-session account-selection RPC and capability. That check failed;
it was not a passing compatibility result. This merge retains those declarations,
native-model reservations, ALLGREEN queue CI changes and their original evidence.
No protocol or migration reservation is reassigned.

The four conflict resolutions preserve both sides' domain/client/protocol
instructions. Worker continuation reads the predecessor's original complete
account/connection pair while retaining the accepted subscription flag for its
private checkpoint provider comparison. API switching remains limited to its
existing explicit stopped-session contract. Go, TypeScript and Connect Query
bindings were regenerated from the reconciled schemas.

## Executed merge checks

On macOS arm64 with Go 1.26.8:

```sh
GOMAXPROCS=4 go test -race -p 2 -timeout 15m \
  ./cmds/delidev-cli/internal/server \
  ./cmds/delidev-cli/internal/worker \
  ./cmds/delidev-cli/internal/cli \
  ./cmds/delidev-cli/internal/domain \
  ./cmds/delidev-cli/internal/integrations/github \
  ./cmds/delidev-cli/internal/apiproxy \
  -run 'Subscription|Managed|Codex.*ProviderMatches|AccountSwitch|SwitchAccount|SwitchSessionAccount|HistoryObservation|NativeHistory|SwitchedHistory|QueueCI|MergeQueue|ALLGREEN' \
  -count 1
```

All six packages passed: server 135.766s, Worker 16.205s, CLI 2.623s,
domain 2.051s, GitHub 3.551s and API proxy 1.795s. An earlier invocation used
incorrect joined Go flags and failed during setup with no Go files at the root;
the corrected command above executed the intended packages.

`pnpm proto:generate`, all six allocation/structure tests and `git diff --check`
passed. Generated files have no remaining unstaged drift against the merged
bindings. The complete post-review race, vet, client, protocol and frontend runs
are recorded separately when complete; this focused check does not claim them.

This adds controlled fixture evidence only. Real-account OAuth/inference,
installed-native/platform/release acceptance, complete uncertain-lease recovery
and issue #964's full desktop scope remain separate.
