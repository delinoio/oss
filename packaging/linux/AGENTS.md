# Linux CLI distribution

- Follow `docs/repository-linux-packages-contract.md` and the root release/environment contracts.
- This directory owns the seven CLI packages, their shared APT keyring package, native repository configuration, and immutable packaging tool/build-image pins.
- All seven enrolled CLIs use stable; keep the reserved preview suite separate, preserve exact upstream versions, and never enable services or modify a user's configuration from package hooks.
- Keep private keys, real credentials, generated package files, and generated repository trees out of Git. Repository-owned `dist` directories are generated and must be removed from the final worktree.
- Verify source release identity and signatures before packaging, and preserve complete signed candidates across retries. Both RPM package signatures and repository metadata signatures are mandatory.
- Production signing and R2 writes belong only to the protected `linux-packages` environment. Tests and dry runs use disposable keys and temporary stores.
- Cloudflare cache rules must be restricted to the package hostname, cache only immutable object paths, bypass mutable entrypoints and refuse negative caching for all error responses.

- Verify signing subkeys against the exact public certificate clients receive before packaging, including after rotation. Include the full license text matching each package's declared license.

- APT CLI packages depend on `delino-archive-keyring`, which owns `/usr/share/keyrings/delino-packages.gpg` in both suites. Certificate updates require an increased keyring version and preserve all historical signing subkeys. Stage with the current signer for at least 30 days before switching; never replace a certificate and signer together.
- Rust bootstrap executables must use the architecture-specific rustup archive URL and checksum in `pins.json`, verified before execution; disable rustup self-updates.

- clibox packages require `ca-certificates` in both formats for OS-trusted HTTPS readiness checks. Preserve Rustls/ring C compilation and the pinned GNU/musl link boundaries; ELF-derived dependencies alone cannot represent certificate data.
