# Issue #1095: unpublished authentication cleanup

Review thread `PRRT_kwDORRAKg86nt1Oz` identified a confirmed pre-native failure
that unnecessarily fenced the unchanged subscription generation. Starting from
`1d15a46908f17fa882826bd73a47b324c1fea72b`, the real Unix permission fixture
denies `CreateTemp` in the fresh private home after protected delivery. No auth
destination or native process is created. On the unfixed source, the test failed
because cleanup returned execution uncertainty instead of the unused original.

The pre-native cleanup path now permits independently confirmed, synchronized
and rechecked destination absence, while retaining the original private-home
and bounded raw/encoded-token scan. Published files still require byte-identical
comparison/removal. Native-owned cleanup still rejects an absent captured file;
missing home, changed published authentication, retained temporary credentials
and linked retained content remain uncertain.

Executed on macOS arm64 using synthetic credentials and temporary state:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/worker \
  -run '^TestManagedExecutionPreNativeFailureRemovesAuthentication/auth-write$' -count 1
# Unfixed source: failed, exit 1.
GOMAXPROCS=2 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/worker \
  -run 'ManagedUnusedAuthentication|ManagedExecution.*Cleanup|ManagedExecutionPreNative' -count 1
# Fixed source: passed, 3.340s.
```

The permission-denial injection is Unix-only and skips elevated users; the
absence/published-file/remnant ownership tests are portable. This does not
establish installed-Codex, Windows or real-account acceptance. The required
complete validation is recorded separately; prior failures remain preserved.
