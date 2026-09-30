# Issue #1095: subscription errors use Runner Device

Review thread `PRRT_kwDORRAKg86nouCo` concerns user-facing execution-device
terminology. Subscription lifecycle and dispatch guidance now use Runner Device
for the selected machine. Technical Worker identifiers, commands, logs and
capability values remain unchanged, following the existing presentation contract.
This is wording only and changes no policy or ownership boundary.

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/server \
  -run 'SubscriptionDispatchRequiresManagedWorkerCapability|SubscriptionCapabilityNegotiates' \
  -count 1
```

Passed on macOS arm64 in 9.175s; `git diff --check` also passed.
