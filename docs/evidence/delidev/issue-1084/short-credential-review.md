# Short proxy credential reflection review repair

## Source and finding

This repair is based on PR #1170 head `3172bd48c7c83f08798956e862988a6194f5f5fd`. Codex thread [PRRT_kwDORRAKg86nbNW6](https://github.com/delinoio/oss/pull/1170#discussion_r4141866282) correctly identified that the existing guard omitted unquoted literal forms shorter than eight bytes, despite the credential contract permitting them. The new tests cover the reported plaintext SSE example.

## Resulting behavior

Short literal, JSON-escaped and Base64 forms match complete byte tokens; ASCII letters/digits/underscore/hyphen and non-ASCII bytes count as token continuations. A complete short candidate remains withheld until another byte or EOF proves its right boundary. The preceding emitted byte preserves left-boundary context across source reads and one-byte caller buffers. Header values use independent complete-value boundaries. Existing exact quoted JSON protection and longer-form substring checks remain intact. Closing a body releases an owned read waiting for a short candidate's right boundary. This finite guard does not claim arbitrary transformation detection or short-form substring matching within unrelated words.

## Executed verification on macOS arm64, 2026-09-30

The following commands use `GOMODCACHE=/private/tmp/delidev-1084-review-modcache`, `GOCACHE=/private/tmp/delidev-1084-review-go-cache` and `GOMAXPROCS=4`, with Go 1.26.8 selected by the repository from the installed Go launcher:

- `go test -race -p 1 ./cmds/delidev-cli/... -run '^(TestNetwork|TestCLINetwork|TestProxyCredential)' -count=1` passed. This filtered run compiles every DeliDev package and executes the selected routing, credential, client, CLI and server regressions; unrelated suites report no tests selected.
- `go test -race -p 1 ./cmds/delidev-cli/internal/outbound -count=1` passed the complete outbound suite after adding independent-header state and pending-boundary cancellation cases. Literal/encoded reflection at response start, punctuation/whitespace boundaries and EOF is rejected with full and one-byte source reads. Unrelated JSON/words and one-byte caller reads remain unchanged.
- `go vet -p 1 ./cmds/delidev-cli/...` passed.
- Initial attempts using the shared cache, and then only a private cache, could not compile after shared cache artifacts and toolchain executables became unavailable. The successful commands isolate both module/toolchain material and cache; they do not modify the shared directories.
- Administrator and ach embed outputs were regenerated explicitly before the required root Go-format commit hook. They are generated output and are removed after verification.

The complete isolated `go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...` retry is still running. It has reported a session acceptance failure while the discovery, Claude and Codex packages have passed. This is not a passing full-suite claim; its final outcome is recorded when complete.

## Evidence limits

These are controlled local fixtures with temporary state and fixture-owned credentials. No real accounts, enterprise proxies, native proxy-credential lifecycle, Windows/Linux runtime, Worker bootstrap or release acceptance was performed. No frontend, protocol or Rust source changed in this repair.
