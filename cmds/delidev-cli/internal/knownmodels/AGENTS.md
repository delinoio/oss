# Known subscription model metadata

- Follow the catalog and network contracts in `docs/` and parent ownership rules.
- Schema 1 contains all three nonempty bounded services and source provenance. Reject invalid, ambiguous or partial data before atomic publication. Filter definite retirement dates only from new recommendations.
- Fetch only the compiled repository-main HTTPS URL through the server outbound route. Keep 15-second/1 MiB limits, 24-hour success/one-hour failure scheduling, private atomic cache and joined shutdown.
- On restart, restore a cache only when its reviewed date is newer than the bundled catalog or its date and semantic version exactly match; an equal-date version mismatch is ambiguous, so prefer the bundle and refresh immediately.
- Never read subscription credentials, run harnesses, create resources or infer account/readiness authority. Log only catalog version/date and stable failure codes. Fixtures use isolated temporary state and injected transports.
