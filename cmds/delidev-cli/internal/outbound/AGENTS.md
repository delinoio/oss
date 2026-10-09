# DeliDev outbound routing

- Follow `docs/cmds-delidev-network-contract.md` and the parent ownership instructions.
- Resolve one immutable server route per HTTP attempt. Preserve destination TLS, bounded CONNECT/SOCKS5 handshakes, cancellation and the callers' existing body/stream limits.
- Never discover ambient proxies, retry a connection, or fall back to Direct or another profile. Exact bypasses are explicit direct authority.
- Proxy credentials stay out of URLs, origin headers and raw diagnostics. Keep finite reflection protection bounded and describe its limits.
- Literal and encoded credential forms shorter than eight bytes require complete token boundaries; retain a complete candidate until the next byte or EOF establishes its right boundary. Preserve the preceding emitted byte across reads and treat independent headers as complete values.
- Check HTTP field names against case-folded protected forms because the HTTP transport canonicalizes them. Credential-bearing routes expose no response trailers or trailer announcements; keep the transport's original response private so EOF cannot repopulate a caller-visible trailer map.
- Fixtures use isolated loopback servers and test credentials; never user accounts or real network infrastructure.

- Plaintext loopback HTTP destinations require Direct or an explicit matching host/IP/CIDR and optional-port bypass. Reject other selected proxy routes before opening any connection; never transmit the account key through CONNECT/SOCKS5 or silently select Direct. Verified HTTPS loopback destinations retain explicit proxy routing.

- Direct/exact-bypass HTTP transports pin localhost to literal IPv4/IPv6 loopback before the caller’s base dialer. Preserve that dialer’s context and the original TLS hostname, ordinary nonlocal DNS and explicit proxy authority; cancellation never selects another profile.

- Proxy credential body guards incrementally decode valid JSON string escapes in names/values and JSON SSE data. Retain only bounded decoded matching suffixes and incomplete escapes with their original wire offsets; preserve allowed wire bytes and short-token boundaries. Rejection closes the original response, and cancellation joins without releasing retained protected prefixes.
