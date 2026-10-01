# Plaintext loopback proxy isolation review evidence

Issue #1084 / PR #1216, reviewed head `b0d4092b1575f9e8b543f1add4494ccfd9062637`, repaired after independent receipt and deletion-cleanup commits.

## Finding and change

[Codex security finding](https://github.com/delinoio/oss/pull/1216#discussion_r4145847077) identified a loopback plaintext HTTP provider passing its account authorization through CONNECT or SOCKS5 to a selected proxy. Loopback-only endpoint validation did not keep that tunnel on the server.

The outbound transport now rejects plaintext loopback HTTP destinations when a non-Direct selected profile lacks an explicit matching bypass. It checks canonical destination/default port before opening a proxy or direct connection. It does not silently change routes. Direct and matching bypasses retain server-local account authority; verified loopback HTTPS remains eligible for explicit proxy routing. The mutable proxy credential buffer is still cleared on every return.

Proxy integration fixtures now use verified TLS origins, with CA roots installed only in test clients. Production trust stores and verification are unchanged. Provider TLS fixtures exercise the production inspection adapter through its internal HTTP client boundary; separate production `Inspect` tests prove plaintext rejection, exact bypass and Direct. Inference fixtures exercise `New` with a test-only CA root on its private transport.

## Validation

`GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/outbound ./cmds/delidev-cli/internal/providers ./cmds/delidev-cli/internal/apiproxy ./cmds/delidev-cli/internal/integrations/github -run 'Network|Credential' -count=1 -timeout=5m` passed twice:

- Initial pass: outbound 2.421s, providers 2.157s, apiproxy 2.267s, GitHub 2.199s.
- Final pass after Direct and wrong-port bypass coverage: outbound 2.070s, providers 2.130s, apiproxy 2.513s, GitHub 2.113s.

New tests cover HTTP/HTTPS/SOCKS5 selected proxies; localhost case variants, default HTTP port, IPv4 loopback, IPv6 loopback and IPv4-mapped loopback; bearer and API-key request headers; zero proxy/direct/origin connections on rejection; wrong-port bypass denial; positive exact-bypass and Direct inspection; positive verified loopback HTTPS proxy routing; real relay rejection and bypass; and preservation of existing redirect refusal, no fallback and joined cancellation checks. `git diff --check` passed.

All accounts, credentials, state and network peers are controlled fixtures. These results do not establish real enterprise proxy, account, OS credential-store or Worker bootstrap acceptance. Combined validation is recorded independently.
