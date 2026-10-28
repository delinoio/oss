# Installation

## Availability

The supplied installation uses a single-host Docker Compose deployment with the web console served from the same origin as the API. A production-ready release, published image coordinates, and an end-to-end installation verification are not established by these guides. Use only the versioned distribution and its matching configuration instructions when they become available. Do not substitute guessed image tags or install an unrelated similarly named package.

## Use a published distribution

Confirm that the [TaiRouter release list](https://github.com/delinoio/oss/releases?q=tairouter%40v) contains the version you intend to deploy. A configured release workflow does not establish that a release has been published or that production installation has passed.

For an available release, download `tairouter-compose.tar.gz`, `SHA256SUMS` and their Sigstore bundles from its `tairouter@v<version>` release. Verify the downloaded archive against the checksum manifest and verify the bundles before extracting it. The archive supplies Compose configuration, an example environment and optional observability settings.

Use `ghcr.io/delinoio/tairouter:<version>` for the matching Linux AMD64/ARM64 server image. It includes the web console. Set `TAIROUTER_IMAGE` in `.env` to that image, preferably pinned by digest, and complete the required configuration below. Pull the image before migration; these commands use the published image without a source build:

```sh
docker compose pull tairouter
docker compose up -d --wait postgres
docker compose run --rm --no-deps tairouter migrate
docker compose up -d --no-build tairouter
```

Retain the previous image digest and a matching database backup for rollback. Publishing an image does not configure your identity provider, reverse proxy or other required services. CLI binaries and the checked npm tarball are separate release assets; confirm npm registry availability before using the npm installer.

## Try locally from source

With Node.js 24, pnpm 10.26.2 and Docker Compose installed, start Docker Desktop or the Docker daemon. In a complete source checkout, run:

```sh
pnpm dev:tairouter
```

The command builds and starts the console, API, PostgreSQL and a local test login/mail service. It generates local configuration and migrates the database automatically. Open `http://127.0.0.1:8080` and select sign-in to log in as the synthetic Alice user without a password. The first build can take several minutes. Keep loopback ports 8080 and 18090 free.

Containers continue running in the background. Use `pnpm dev:tairouter:logs` to view logs and `pnpm dev:tairouter:stop` to stop the local installation while keeping its data and keys. Running the start command again reuses that state.

This mode is for local evaluation. Do not expose the test login publicly. Mail is discarded, no real AI provider is preconfigured, and generated image/video storage requires real R2 configuration. Production deployment still requires the services and checks below.

## Install the console as an app

Open `/overview` on your TaiRouter instance over HTTPS. Loopback HTTP addresses can be used for local evaluation. In a supported browser, use its **Install app** control or menu command. On iPhone or iPad, open the console in Safari, choose **Share → Add to Home Screen**, and enable **Open as Web App** if offered. Menu names and installation availability depend on the browser and operating system.

Launch TaiRouter from the installed app icon. It opens the overview in a standalone window and uses the same instance and sign-in flow. Sign in when prompted. Installation does not grant permissions or install the server or CLI. The console requires a network connection; offline management is unavailable.

Browser and device installation, including sign-in through an external identity provider, requires verification on your target platforms. A source build alone does not establish those results.

## Prepare the host

1. Choose Linux x86_64 or ARM64 for the server. Docker Desktop on macOS and Windows is for development or evaluation.
2. Prepare Docker Compose, persistent PostgreSQL storage, and an existing HTTPS reverse proxy.
3. Choose a public HTTPS origin, such as `https://router.example.com`. This is a placeholder, not a hosted service.
4. Prepare a generic OIDC provider, SMTP delivery, a private Cloudflare R2 bucket, and separately stored versioned encryption keys.
5. Identify initial instance operators by their exact OIDC issuer and subject. Organization ownership does not grant operator access.
6. Decide backup frequency, retention, storage access, and a maintenance window. TaiRouter does not schedule backups for you.

## Configure and start

Follow [service configuration](configuration.md) for the established configuration names. Keep credentials outside version control and separate from public documentation or diagnostic attachments.

The server interface defines `tairouter-server migrate` and `tairouter-server serve`. These names alone are not a complete installation procedure: use the released Compose configuration to supply the database, secrets, network, console, and image versions. Do not start a partially configured service for production traffic.

Before opening the public proxy, verify database readiness, encryption-key availability, OIDC callback registration, SMTP delivery, and R2 access in an isolated evaluation deployment. Check login, organization selection, a personal provider connection, and a client request. Inspect failures without recording request content or credentials.

## Compose startup interface

The current Compose assets define the following startup sequence. Run it from the directory containing the matching `compose.yml` and populated `.env`. Distribution download instructions and a production installation result remain unverified.

First copy the supplied `.env.example` to `.env`, restrict it to owner access, and replace every placeholder and evaluation-only address. Set `POSTGRES_PASSWORD` to a random URL-safe password. Set `GRAFANA_ADMIN_PASSWORD` if using the observability profile. Set `TAIROUTER_IMAGE` to the exact image selected for this deployment; do not assume a published registry tag exists.

```sh
cp ".env.example" ".env"
chmod 600 ".env"
```

Edit the file securely before running the following commands. Do not start with its loopback identity-provider or R2 fixture values.

```sh
docker compose build
docker compose up -d --wait postgres
docker compose run --rm --no-deps tairouter migrate
docker compose up -d tairouter
```

The build requires the complete source context supplied with the Compose assets; copying the Compose file alone does not create a buildable distribution. These commands establish the packaging interface, not acceptance of the console, providers, or deployment. Keep external traffic blocked until the checks above pass.

The Compose service publishes HTTP only on host loopback port 8080 and does not publish PostgreSQL. The proxy must run where it can reach that listener. Do not change it to a public bind to repair connectivity.

## Publication boundaries

The public documentation can be hosted as a static site on Cloudflare Pages. That does not deploy the TaiRouter service. The canonical documentation address is `https://docs.labs.delino.io/tairouter/`. This address does not provide a hosted TaiRouter service.

The client executable is `tairouter`, with standalone binaries and checksums and an npm installer named `@delino/tairouter`. Package publication and download availability must be confirmed for a release before installation. See [client prerequisites](clients.md).
