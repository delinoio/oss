# Response metadata credential isolation

Codex thread `PRRT_kwDORRAKg86nhybN` identified two unguarded response
surfaces: HTTP field names and trailers populated after body EOF. The repair
checks field names using the existing finite protected forms and token
boundaries, case-folded independently of body/value matching because HTTP
canonicalizes field names.

Credential-bearing routes return a separate response object with no trailer
map or announcement. The standard transport retains its original response
privately and may populate that object's trailers during body reads. Clearing
only the original map before returning would not suffice: EOF can allocate or
repopulate it. No client trailer authority is introduced, and ordinary headers
and streaming body behavior are preserved.

Loopback CONNECT fixtures reject canonicalized literal/encoded credential
field names with one transmission. Declared and undeclared trailer names and
values remain private before and after EOF; unrelated body/header content is
preserved. Short-form tests retain complete-token semantics for field names.
These are controlled fixtures, not real proxy acceptance or arbitrary
transformation detection. See the [Go response contract](https://pkg.go.dev/net/http#Response)
for trailer publication timing.

`GOMAXPROCS=2 go test -race -p 2` with `-count=1 -timeout 5m` across outbound,
provider, inference-proxy and GitHub packages passed after the transport repair.
The final complete race command after both review repairs is recorded
separately; the failed pre-review merge command remains historical evidence.
