TaiRouter provides a self-hosted service and client configuration CLI.

- Download the CLI binary for your platform and verify `SHA256SUMS` and the Sigstore bundles.
- Download `tairouter-compose.tar.gz` for the Compose configuration, example environment and observability assets.
- Set `TAIROUTER_IMAGE=ghcr.io/delinoio/tairouter:<version>` in your deployment environment. Configure PostgreSQL, OIDC, SMTP, R2 and encryption keys before starting the service. The image includes the web console and supports Linux AMD64 and ARM64.
- Use your reverse proxy for HTTPS. Run database migrations before admitting traffic. Back up before upgrades and retain the matching image and database backup for rollback.

Installation and recovery guidance: https://docs.labs.delino.io/tairouter/guide/installation

Published assets include Apache-2.0 terms and third-party notices. Cross-platform builds and synthetic integration/load fixtures do not establish live-provider, production-deployment or native macOS/Windows acceptance. npm publication is separate and may remain disabled until publishing authority and the trusted publisher are configured.
