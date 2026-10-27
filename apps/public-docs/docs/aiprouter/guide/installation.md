# Installation

## Availability

The supplied installation uses a single-host Docker Compose deployment with the web console served from the same origin as the API. A production-ready release, published image coordinates, and an end-to-end installation verification are not established by these guides. Use only the versioned distribution and its matching configuration instructions when they become available. Do not substitute guessed image tags or install an unrelated similarly named package.

## Prepare the host

1. Choose Linux x86_64 or ARM64 for the server. Docker Desktop on macOS and Windows is for development or evaluation.
2. Prepare Docker Compose, persistent PostgreSQL storage, and an existing HTTPS reverse proxy.
3. Choose a public HTTPS origin, such as `https://router.example.com`. This is a placeholder, not a hosted service.
4. Prepare a generic OIDC provider, SMTP delivery, a private Cloudflare R2 bucket, and separately stored versioned encryption keys.
5. Identify initial instance operators by their exact OIDC issuer and subject. Organization ownership does not grant operator access.
6. Decide backup frequency, retention, storage access, and a maintenance window. AIPRouter does not schedule backups for you.

## Configure and start

Follow [service configuration](configuration.md) for the established configuration names. Keep credentials outside version control and separate from public documentation or diagnostic attachments.

The server interface defines `aiprouter-server migrate` and `aiprouter-server serve`. These names alone are not a complete installation procedure: use the released Compose configuration to supply the database, secrets, network, console, and image versions. Do not start a partially configured service for production traffic.

Before opening the public proxy, verify database readiness, encryption-key availability, OIDC callback registration, SMTP delivery, and R2 access in an isolated evaluation deployment. Check login, organization selection, a personal provider connection, and a client request. Inspect failures without recording request content or credentials.

## Compose startup interface

The current Compose assets define the following startup sequence. Run it from the directory containing the matching `compose.yml` and populated `.env`. Distribution download instructions and a production installation result remain unverified.

First copy the supplied `.env.example` to `.env`, restrict it to owner access, and replace every placeholder and evaluation-only address. Set `POSTGRES_PASSWORD` to a random URL-safe password. Set `GRAFANA_ADMIN_PASSWORD` if using the observability profile. Set `AIPROUTER_IMAGE` to the exact image selected for this deployment; do not assume a published registry tag exists.

```sh
cp ".env.example" ".env"
chmod 600 ".env"
```

Edit the file securely before running the following commands. Do not start with its loopback identity-provider or R2 fixture values.

```sh
docker compose build
docker compose up -d --wait postgres
docker compose run --rm --no-deps aiprouter migrate
docker compose up -d aiprouter
```

The build requires the complete source context supplied with the Compose assets; copying the Compose file alone does not create a buildable distribution. These commands establish the packaging interface, not acceptance of the console, providers, or deployment. Keep external traffic blocked until the checks above pass.

The Compose service publishes HTTP only on host loopback port 8080 and does not publish PostgreSQL. The proxy must run where it can reach that listener. Do not change it to a public bind to repair connectivity.

## Publication boundaries

The public documentation can be hosted as a static site on Cloudflare Pages. That does not deploy the AIPRouter service. No public documentation domain or hosted service is promised here.

The client executable is `aiprouter`, with standalone binaries and checksums and an npm installer named `@delino/aiprouter`. Package publication and download availability must be confirmed for a release before installation. See [client prerequisites](clients.md).
