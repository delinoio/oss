# DeliDev owned native API tunnel

- Follow the network and native API relay contracts. This package owns one ephemeral authenticated loopback listener for one pinned Codex API runtime, never a public RPC or generic proxy.
- Authenticate before dialing and accept only the exact original paired server host/port. HTTPS stays an end-to-end CONNECT tunnel; plain HTTP permits only the original loopback origin and fixed Responses relay paths.
- Keep upstream credentials in bounded Go memory and local credentials in the private native environment only. Do not persist, log or serialize either. Exact bypass selection belongs exclusively to the Go profile; no retries, ambient discovery or failure fallback.
- Bound local connections, headers, copy buffers and deadlines. Cancel and join every listener, socket, tunnel and observation with the original runtime. Listener readiness does not establish native route use or inference success.
- Use isolated controlled proxies and temporary state in tests; never real accounts, system trust mutations or user credential stores.
