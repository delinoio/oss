# Manual migration

## Previous AIPRouter installations

TaiRouter replaces the AIPRouter name. The CLI is `tairouter`, the npm installer is `@delino/tairouter`, configuration uses `TAIROUTER_*`, and management RPCs use `tairouter.v1`. Update the server, console, and CLI together. Previous command names, environment variables, and management RPC paths are not aliases for the new names.

Existing database schemas, local evaluation volumes, CLI profiles, credential-store entries, and background services are not migrated automatically. Keep the old installation and its encryption keys and protected backups available. Use the old CLI to disconnect clients and restore owned settings before configuring new TaiRouter connections. Do not point the renamed service at an existing installation's database and assume its data will be migrated. Evaluate a separate installation and recreate connections before cutover. An upgrade that preserves the old installation's data requires a separate migration procedure.

## Migration from opencodex

TaiRouter does not automatically import opencodex settings, credentials, routing, or usage. There is no user-facing data export. Migration is manual and must wait until the required integrations are available in your target release.

## Prepare

1. Check [compatibility](compatibility.md) for each provider, authentication method, protocol, and client you need. A matching feature name is insufficient.
2. Keep your existing setup available while evaluating TaiRouter. Protect any local settings backups, which may contain secrets.
3. Ask the operator to establish installation, OIDC, SMTP, TLS, R2, and tested recovery.
4. Identify which organization each connection belongs to. Personal providers and routes must be registered separately in each organization.

## Recreate connections

1. Sign in with your verified OIDC identity and create or join the intended organization.
2. Register your own provider credentials again or complete each supported provider OAuth flow.
3. Recreate personal account pools, model selection, routes, and combinations using the target release's supported interfaces.
4. Issue new TaiRouter keys or approve dedicated CLI connections. Do not reuse the old router's keys as TaiRouter keys.
5. Preview and confirm client changes, including separate consent for remote keys placed in client settings.
6. Test non-sensitive requests, streaming, tools, errors, and continuation for the actual client/provider pairs you use.

Existing conversations and usage history do not migrate. Begin new conversations explicitly. Keep any needed temporary output under your own approved handling policy; TaiRouter does not provide permanent image or video retention.

## Cut over or return

Switch clients only after evaluating the required interfaces. Watch errors and unknown usage during the change. When satisfied, revoke obsolete keys and disconnect old connections deliberately.

If an integration is unavailable or fails, preserve the old setup and use the client's protected settings restoration procedure. Reconcile edits rather than overwriting conflicts. Stop TaiRouter relays and revoke dedicated keys; if the server is offline, complete web revocation later. Do not migrate excluded local-program provider execution to the server as a workaround.

## Optional native mode migration

Ordinary gateway configuration is sufficient for the supported gateway workflow; optional native TLS modes are a separate choice. Do not treat earlier provider/key consent as authorization to install a certificate or mutate native application settings. Review [native compatibility](native-compatibility.md), including exact-build restrictions and restoration, before changing modes. Do not copy old trust certificates, private keys, proxy settings, or compatibility state into TaiRouter.
