# DeliDev outbound routing

- Follow `docs/cmds-delidev-network-contract.md` and the parent ownership instructions.
- Resolve one immutable server route per HTTP attempt. Preserve destination TLS, bounded CONNECT/SOCKS5 handshakes, cancellation and the callers' existing body/stream limits.
- Never discover ambient proxies, retry a connection, or fall back to Direct or another profile. Exact bypasses are explicit direct authority.
- Proxy credentials stay out of URLs, origin headers and raw diagnostics. Keep finite reflection protection bounded and describe its limits.
- Fixtures use isolated loopback servers and test credentials; never user accounts or real network infrastructure.
