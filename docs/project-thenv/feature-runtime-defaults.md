# Feature: runtime-defaults

## Runtime Defaults
Environment template files:
- Server: `servers/thenv/.env.example`
- Devkit web console proxy: `apps/devkit/.env.example`

Server environment variables:
- `THENV_ADDR` (default: `127.0.0.1:8087`)
- `THENV_DB_PATH` (default: `${XDG_CONFIG_HOME or OS config dir}/thenv/thenv.sqlite3`)
- `THENV_MASTER_KEY_B64` (required, base64-encoded 32-byte key)
- `THENV_BOOTSTRAP_ADMIN_SUBJECT` (default: `admin`)

CLI environment variables:
- `THENV_SERVER_URL` (default: `http://127.0.0.1:8087`)
- `THENV_TOKEN` (default: `admin`)
- `THENV_SUBJECT` (optional; defaults to `THENV_TOKEN` value, and must match token for server authorization)

Devkit environment variables (optional):
- `THENV_SERVER_URL` or `NEXT_PUBLIC_THENV_SERVER_URL`
- `THENV_WEB_TOKEN` or `THENV_TOKEN` or `NEXT_PUBLIC_THENV_TOKEN`
- `THENV_WEB_SUBJECT` or `THENV_SUBJECT` or `NEXT_PUBLIC_THENV_SUBJECT` (defaults to resolved token value and must match token for server authorization)

