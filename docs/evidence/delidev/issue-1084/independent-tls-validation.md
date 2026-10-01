# Independent HTTPS proxy and destination verification

Starting from implementation commit `8cf47f161`, the HTTPS fixture now retains the
proxy certificate separately from the origin. The previously reused failure case
removed both roots and could fail at the proxy before exercising destination TLS.

The corrected fixture performs three independent observations for HTTPS routing:

1. Trust both endpoints and complete exactly one origin request through CONNECT.
2. Trust only the HTTPS proxy, reach its second CONNECT, and reject the untrusted
   destination without a direct attempt.
3. Trust only the destination and reject the HTTPS proxy before another CONNECT.

HTTP CONNECT and SOCKS5 retain their independent untrusted-destination checks.
No transport or production trust policy changes are needed. Fixture certificates
exist only in memory; normal hostname and CA verification remain enabled.

`GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/outbound -count=1 -timeout 3m`
passed on 2026-09-30, including the reflection, bypass, ambient-isolation,
no-fallback, cancellation and independent TLS cases. This is loopback fixture
verification, not enterprise proxy or native/account acceptance.

The post-commit `pnpm proto:check` at implementation commit `8cf47f161` also passed
lint, baseline compatibility and committed-output freshness. Generated sources
were unchanged after regeneration.
