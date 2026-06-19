# Reference

## Stable Behavior

Nodeup documentation must preserve these user-facing contracts:

- Stable channel naming and runtime dispatch semantics.
- Deterministic shim behavior across supported operating systems.
- Deterministic shell completion generation for supported shells and top-level command scopes.
- Stable human output styling controls through `--color`, `NODEUP_COLOR`, and `NO_COLOR` precedence.

## Release Artifacts

Release automation must publish standalone prebuilt binaries and archive assets for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Each artifact must have a Sigstore bundle sidecar and must be covered by `SHA256SUMS`.
