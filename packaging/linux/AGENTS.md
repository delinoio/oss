# Linux CLI distribution

- Follow `docs/repository-linux-packages-contract.md` and the root release/environment contracts.
- This directory owns only the six CLI packages, native repository configuration, and immutable packaging tool/build-image pins.
- Keep stable and preview separate, preserve exact upstream versions, and never enable services or modify a user's configuration from package hooks.
- Keep private keys, real credentials, generated package files, and generated repository trees out of Git. Repository-owned `dist` directories are generated and must be removed from the final worktree.
- Verify source release identity and signatures before packaging, and preserve complete signed candidates across retries. Both RPM package signatures and repository metadata signatures are mandatory.
- Production signing and R2 writes belong only to the protected `linux-packages` environment. Tests and dry runs use disposable keys and temporary stores.
- Cloudflare cache rules must be restricted to the package hostname, cache only immutable object paths, bypass mutable entrypoints and refuse negative caching for all error responses.
