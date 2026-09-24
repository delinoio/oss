# DeliDev account lifecycle

## Ownership and implemented scope

The server owns account state and credentials under [issue #964](cmds-delidev-requirements.md). This contract currently implements API credential connection, explicit keyless local connection, disconnection, cleanup reconciliation and account status through authenticated Connect and the CLI. Bounded non-inference validation is defined in the [provider inspection contract](cmds-delidev-providers-contract.md). Automatic model catalog publication is defined in the [catalog contract](cmds-delidev-catalog-contract.md). Digest-only execution proxy credentials and first Codex Worker execution are integrated through the [proxy](cmds-delidev-proxy-contract.md) and [session](cmds-delidev-sessions-contract.md) contracts. Disconnect now durably cancels that account's unfinished native assignments; public first Codex API dispatch uses current validated connection readiness; complete native recovery remains pending alongside subscription login/logout/import and quota refresh. Saving a credential is not provider validation or execution readiness.

Account aliases/provider associations and display/routing preferences remain configuration. Health, connection generation, validation/catalog observations, quota observations and pending removal are server-owned. General configuration writes must preserve those fields exactly; new accounts start disconnected. An account's provider/type cannot be relabeled through configuration, and a referenced provider's authentication/authority cannot be changed in place. Credentials never enter configuration documents.

## CLI and RPC

| CLI | Connect RPC | Meaning |
| --- | --- | --- |
| `account connect --id ID --revision N --key-stdin` | `AccountService.ConnectAccount` | Store a bounded API key through the protected server vault and record an unverified connection. |
| `account connect --id ID --revision N --keyless` | `AccountService.ConnectAccount` | Explicitly connect a provider already configured with keyless local authentication; no native secret is written. |
| `account disconnect --id ID --revision N` | `AccountService.DisconnectAccount` | Commit disconnection, remove every remaining protected reference owned by the account, then clear the cleanup marker. |
| `account status --id ID` | `AccountService.GetAccountStatus` | Read current metadata without consulting or unlocking the native credential store. |
| `account validate --id ID --revision N` | `AccountService.ValidateAccount` | Record a bounded non-inference endpoint/credential observation for the current connection. |

Mutations accept `--request-id UUID-V7` and return the existing version-1 CLI envelope. They never start a server implicitly. Worker credentials cannot invoke any account RPC. Owner and paired client authorization is rechecked before secret staging, at state/receipt commit and before cleanup completion; revoking an in-flight client cannot publish a connection after a native write.

API keys contain 1–8,192 printable non-space ASCII bytes. The CLI accepts one optional LF or CRLF terminator from stdin and rejects extra lines, whitespace, control characters, oversized input and empty input. A terminal without supplied stdin returns missing input instead of requesting interactive authorization. There is no API-key argument or generic configuration-file key field. `--key-stdin` cannot share stdin with `--token-stdin`; use the private owner scope or a paired client. Both sides clear their decoded secret buffers after bounded use. These measures do not promise complete heap erasure or an OS sandbox.

Provider authentication determines whether a key or explicit keyless selection is valid. Keyless providers remain loopback-only, and localhost means the server machine. A subscription account receives an explicit unsupported response from this API operation; it cannot use an unrelated system login or reinterpret an API key as subscription authentication.

## Connection and readiness

A connection requires the current account revision, disconnected health and no pending cleanup. A new immutable connection generation equals the connect request UUID-v7. Its metadata contains the provider's authentication mode and connection timestamp; the raw key remains in the [protected vault](cmds-delidev-credentials-contract.md). Connection sets `health=unverified`, clears prior validation/catalog and quota/exhaustion observations and does not grant routing eligibility. The routing boundary accepts only ready accounts with a current connection and no pending removal, so a stored credential cannot authorize execution.

An explicit disconnect with completed cleanup is required before replacing a connection. Connection metadata cannot be changed through an ordinary configuration edit. Native/file work runs outside SQLite transactions. The server serializes account connection, disconnection and configuration deletion/edit boundaries; competing connection requests with the same expected revision cannot stage multiple active connections.

Before staging credentials, the server checks a read-only durable receipt lookup and validates authorization, account revision, type, provider and state. The final SQLite mutation repeats the checks and commits connection metadata, events and the receipt together. If a native write succeeds but SQLite acceptance fails or is uncertain, the reference remains staged. It is never speculatively deleted: retry the same request or explicitly disconnect to reconcile all of that account's protected intents. An account with outstanding intents cannot be deleted through general configuration.

The connect receipt input binds the exact secret with HMAC-SHA256 using the server's existing private durable owner identity, with a distinct account-connect domain, server scope and request identity. SQLite retains neither raw key bytes nor an unkeyed secret digest. This request commitment remains comparable after the per-credential wrapping key has been deleted. Restoring a scope must preserve its matching private owner identity; changing that identity is not a credential rotation API.

## Disconnection and retry

Disconnection has two durable state changes:

1. The acceptance transaction clears the connection, sets disconnected health, clears validation/catalog and quota/exhaustion observations, cancels unfinished assignments selected on that account and writes a removal marker plus its receipt. The public marker contains the original request ID and expected revision, so a client can reconstruct an exact retry from account status. A separate private cleanup receipt identity is retained only inside the server receipt, never in public account metadata.
2. Outside the transaction, the server enumerates only the account's own unremoved vault references, including interrupted staging, and deletes them through the vault's durable tombstones. A second transaction rechecks authorization and the pending disconnect identity, preserves any newer metadata edits, removes the marker and emits the final revision/event.

Execution cancellation is paged over exact selected-account assignments, including pre-grant queued work and uncertain ownership. Candidate routing links, other accounts and terminal history are excluded. Undispatched jobs become canceled; claimed envelopes retain their exact revision/digest and receive a separate durable cancellation control, including on reconnect. Sessions pause and retain execution/input ownership. Existing uncertainty stays recoverable, and cancellation alone neither completes Archive nor proves native acceptance or process cleanup. Old disconnect receipts never recancel work created on a replacement connection.

Before key deletion the server cancels and joins active relay handlers. The Worker independently receives its cancellation, joins owned native cleanup and journals/reports its outcome. Missing native terminal/cleanup publication retains session recovery even when relay/key cleanup succeeds; these are separate confirmations.

No keychain is silently unlocked and no unrelated native credential is enumerated. If the native store, filesystem or transaction is unavailable, the account remains disconnected with a retryable removal marker. `DisconnectAccount` returns an accepted disconnection response containing current account metadata and a typed sanitized cleanup problem. The CLI keeps that result and request ID while returning the problem's nonzero exit code. Retry the original `account disconnect` request with the original expected revision and request ID; a new request while removal is pending conflicts and points to the recorded operation.

Successful cleanup permits reconnection or configuration deletion. A completed deletion tombstone no longer blocks account deletion, but remains durable in the private vault. Deletion still validates every configuration/session/schedule reference. A metadata edit made while cleanup is waiting must survive the original disconnect's later completion.

Receipt replay never repeats native staging or deletes a replacement generation. Replaying an old connect after disconnect returns current disconnected metadata without recreating credentials. Replaying an old disconnect after a later connection leaves the new connection untouched. A deleted account cannot be recreated by any of its previous requests. Reads and replays retain current revocation checks. Server shutdown waits for the account operation boundary and closes its owned vault before releasing the data scope or recording the final stopped event.

## Verification

Real loopback Connect and temporary SQLite tests cover exact retries across restart, different-secret conflicts, concurrent connections, keyless/type/authentication guards, forbidden configuration health changes, Worker denial, client revocation during native staging, orphan-intent deletion guards, failed cleanup across restart, later metadata edits and old receipts after reconnection/deletion. The secret backend used for fault injection exists only in tests. Production always uses the OS-backed vault.

The CLI's keyless workflow is tested through the real server, and secret-input tests cover bounds, line endings and conflicting stdin consumers. A separately opted-in native CLI test runs the compiled CLI/server with real Linux Secret Service inside the disposable container described in the credential storage contract. It verifies protected stdin input, restart/replay, disconnection, deletion and secret-free retained server files/output. It makes no provider/model/inference requests.

To run that native CLI check after building the credential test image, build the CLI and test binary for the Docker daemon's native architecture into a fresh temporary directory, and mount only those test artifacts read-only:

```sh
delidev_cli_fixture_dir=$(mktemp -d)
chmod 755 "$delidev_cli_fixture_dir"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$delidev_cli_fixture_dir/delidev" ./cmds/delidev-cli
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c ./cmds/delidev-cli/internal/cli -o "$delidev_cli_fixture_dir/cli.test"
cp cmds/delidev-cli/internal/cli/testdata/secret-service/run.sh "$delidev_cli_fixture_dir/"
docker run --rm --network none --mount "type=bind,source=$delidev_cli_fixture_dir,target=/opt/delidev-tests,readonly" --entrypoint dbus-run-session delidev-secret-service-test:local -- /opt/delidev-tests/run.sh
rm -rf "$delidev_cli_fixture_dir"
```

Choose `amd64` instead when appropriate. This opt-in test must never target a host user's shared credential session. Native Windows/macOS account-command composition, subscription/provider authentication and execution/proxy lifecycle evidence remain separate from this Linux credential-lifecycle result. Consult the [evidence ledger](cmds-delidev-evidence.md) before making a support/completion claim.
