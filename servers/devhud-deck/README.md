# DevHud Deck server

`servers/devhud-deck` is the private Go/PostgreSQL implementation of the
`devhud.deck.v1` Connect contract. It currently implements authenticated view
and device persistence, current viewer-scoped PR snapshots, typed audits,
health endpoints, owner/lifecycle deletion, signed GitHub.com App/OAuth
callbacks, installation lifecycle handling, permission-filtered provider
search and candidate discovery, expiring user-token rotation, authorization
revocation handling, the closed user-attributed mutation set, and
client-initiated provider refresh. Refresh uses five-minute cache/coalescing,
live forwarded-user delibase reserve/commit/release at 50 USD micros,
current encrypted snapshots, widget freshness, and 30-day notification
transition history/resolution. Push delivery remains unimplemented.

The process has no scheduler, provider-polling worker, billing worker, or
post-client provider continuation. Nonterminal accounting advances only on an
active exact client retry with a fresh forwarded-user bearer. Nothing in this
directory deploys an API, configures DNS, publishes a client/SDK or image,
registers a GitHub App, activates a catalog meter, or supplies remote UI.

Generate sqlc code and run checks from the repository root:

```sh
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0
servers/devhud-deck/scripts/generate-sqlc.sh
go test ./servers/devhud-deck/...
go vet ./servers/devhud-deck/...
```

PostgreSQL integration tests use `DECK_TEST_DATABASE_URL` and are skipped when
that variable is absent.

The GitHub App manifest and signature payloads under `testdata/github-app` are
fixtures only. They must not be used to register a production App.

Logout and reset remain client operations. Both unregister the current device
before deleting local credentials; reset does not call feature deletion and
therefore does not delete server views or connections.
