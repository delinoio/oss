# API keys and clients

## Availability

The executable is `tairouter`. The commands below are implemented; publication of downloadable packages and live external-client acceptance are not established here. Run `tairouter help` and `tairouter clients` to inspect the installed version. Flags follow the command. There is no noninteractive auto-accept option.

Target CLI platforms are Linux x86_64/ARM64, macOS x86_64/ARM64, and Windows x86_64. A CLI build for an operating system does not mean every external client supports it. Confirm release availability and checksums before installing standalone binaries. The planned npm installer name is `@delino/tairouter`; publication is not confirmed here.

## Connect and configure

Replace the example HTTPS origin with your operator's address. `work` is an example profile name. The browser approval selects the organization and expiry; there is no organization flag to invent.

```sh
tairouter connect --profile work --server https://router.example.com --device-name workstation --port 17843
tairouter profiles
tairouter clients
tairouter configure --profile work --client codex
```

`connect` obtains and stores the dedicated key. `configure` then previews changes for the named client and asks for confirmation. It separately asks before placing a remote key in client settings. Close Cline or Claude Desktop before confirming their paired-file changes. Use the client IDs below exactly.

`--state-dir` selects an absolute state directory. If you use it, supply the same directory to subsequent commands. State contains metadata and protected recovery backups, not a plaintext credential fallback. `NO_COLOR=1` disables ANSI log colors.

## Issue an inference key

In the selected organization, use the key workflow to create a personal key with no expiry or a chosen expiry. Copy the raw key when shown; ordinary issuance shows it once. Store it securely. The key belongs to that user and organization and grants inference access, not general management access.

Revoke a lost or exposed key through the organization's key management view. Revocation/expiry blocks new requests immediately. A response already dispatched upstream may finish, but no further retries, account switches, or refreshes are permitted for it.

## Approve a CLI connection

1. Run `connect` as shown above. The CLI displays an approval URL and one-time code; the browser can be on another computer for SSH use.
2. Open the URL, log in, and compare the code and server address. Confirm the organization, device name, and key expiry before approving.
3. Allow the CLI to finish storing its dedicated key in the OS credential store.
4. Review the proposed client-settings changes and confirm them.
5. If a client requires a remote key in its own settings, give separate explicit confirmation for that additional storage.

Codes expire ten minutes after original issuance. Polling occurs every five seconds. Denial or expiry before approval creates no key. Interrupted delivery can be retried by the same CLI until the original expiry. Unclaimed keys are revoked at expiry.

If the OS credential store is unavailable, settings must remain unchanged; a normal credential file is not a fallback. The CLI must attempt to revoke an already-issued key. If that fails, sign in to the web console and revoke the reported outstanding key manually.

## Profiles and relay trust

Named profiles keep separate server/organization credentials. Select an active profile for each client. Switching profiles retains the prior profile's key; disconnecting a profile revokes its key.

Some clients require an HTTP relay installed as a user background service that starts at login. It binds only to loopback and has no separate local authentication: other processes on that computer can call it. Use this only under that local-computer trust policy. It does not execute client programs as providers.

Concurrent profile relays require distinct fixed ports. A conflict must fail with an actionable error, not silently select another port. Changing a port requires a preview and confirmation of affected settings. The default HTTP relay port is 17843. Choose a distinct `--port` at connection time for each additional profile.

```sh
tairouter service --profile work --action install
tairouter service --profile work --action status
tairouter service --profile work --action stop
tairouter service --profile work --action remove
```

For foreground operation, use `tairouter relay --profile work`. To change the profile port, use `tairouter port --profile work --port 17845` and review the proposed settings changes. Port changes do not authorize optional certificate trust or native application changes.

## Disconnect and recover settings

```sh
tairouter disconnect --profile work
tairouter restore --client codex
tairouter recover
```

These are separate recovery actions, not a sequence to run blindly. `disconnect` offers restoration and attempts revocation. `restore` can restore a client's protected backup even after the profile was removed. Use `recover` when the CLI reports an interrupted settings transaction; review and confirm recovery rather than deleting its state. Conflicting user edits require manual reconciliation.

Disconnect attempts server-key revocation, stops the relay, and offers restoration from protected original-settings backups. It must preserve unrelated settings and your later edits. Successful restoration deletes the recovery backup.

If your edits conflict, preserve both the edited settings and recovery backup, then reconcile them manually using the reported conflict. Do not overwrite the whole file. Key revocation and relay shutdown must still be attempted. Backups can contain original secrets: keep owner-only permissions or Windows ACLs and never attach them to support requests.

If the server is offline, local cleanup can proceed, but server revocation remains incomplete. When access returns, sign in to that server and organization and revoke the dedicated key. A restored server also requires fresh login, keys, and CLI connection; see [recovery](recovery.md).

## Client configuration inventory

The table describes implemented configuration handling, not tested acceptance of the external applications. The preview reports the resolved files. Existing client installations and supported client versions are prerequisites; an TaiRouter platform build is not evidence that a client runs on that platform. Unsupported formats, unsafe links, ambiguous ownership, or competing settings fail without a lossy whole-file fallback.

| Client IDs                  | Configuration and important constraints                                                                     |
| --------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `qoder`, `qoder-cn`         | JSON settings in their separate homes; `QODER_CONFIG_DIR` / `QODERCN_CONFIG_DIR`; HTTP relay                |
| `workbuddy`, `workbuddy-ai` | Separate JSON model stores; `WORKBUDDY_CONFIG_DIR`, then `CODEBUDDY_CONFIG_DIR`; HTTP relay                 |
| `opencode`                  | JSON provider blocks in the XDG configuration home; direct key consent                                      |
| `pi`, `prime`, `omo`        | JSON model catalogs using each client's agent-directory selectors; HTTP relay                               |
| `omp`                       | YAML model catalog and selected `OMP_PROFILE` / `PI_PROFILE`; HTTP relay                                    |
| `hermes`                    | YAML configuration; `HERMES_HOME` and discovered live named profiles; direct key consent                    |
| `openclaw`                  | JSON5 with native config/state/home/profile selectors and legacy candidate checks; direct key consent       |
| `kimi`                      | TOML under `KIMI_CODE_HOME`; only models with authoritative context limits; HTTP relay                      |
| `gajae`                     | YAML model configuration; HTTP relay                                                                        |
| `dsh`                       | YAML settings or active Desktop profile patch; missing active patch refuses; HTTP relay                     |
| `mcode`                     | YAML configuration using `MINIMAX_DATA_DIR`, then `MAVIS_DATA_DIR`; Messages through HTTP relay             |
| `zcode`                     | Bootstrap/data-directory and selected personal provider store; unknown schema refuses; HTTP relay           |
| `aside`                     | Registered account catalogs and canonical root alias; no guessed account; HTTP relay                        |
| `raycast`                   | YAML provider entries, macOS/Windows; HTTP relay                                                            |
| `cline`                     | Paired JSON provider/model catalogs; stop the client first; HTTP relay                                      |
| `commandcode`               | JSON provider store preserving singular/plural provider roots; HTTP relay                                   |
| `kilo`                      | JSONC/JSON5 candidate detection and recorded ownership; competing providers refuse; direct key consent      |
| `droid`                     | JSON custom models with competing-settings checks; HTTP relay                                               |
| `gentle-shell`              | Isolated/custom home and Pi-derived configuration; linked/shared homes refuse; HTTP relay                   |
| `codex`                     | Native TOML provider selection and merged model catalog/cache under `CODEX_HOME`; direct key consent        |
| `claude`                    | Gateway settings and native gateway-model cache under `CLAUDE_CONFIG_DIR`; direct key consent               |
| `grok`                      | TOML provider and collision-safe model aliases/reasoning choices under `GROK_HOME`; HTTP relay              |
| `claude-desktop`            | Third-party profile and active-selection metadata, macOS/Windows; stop the client first; direct key consent |

Use `tairouter client-profiles --client hermes` to inspect discoverable client profiles. TaiRouter connection profiles and a client's own profiles are separate concepts.

Codex ordinary configuration preserves native provider identity, merges service catalog data with retained native rows, and invalidates the owned model cache. WebSocket capability is advertised only when exact service metadata supplies it. Foreign selected providers or conflicting owned settings refuse. Claude ordinary configuration updates its gateway model cache; Desktop selects a dedicated third-party profile while preserving the previous selection for restoration.

There is no unattended catalog refresher. Rerun `configure` to preview refreshed settings/catalogs. Source-preserving YAML flow-map and flow-sequence insertion is supported; shared anchored containers and unsupported alias shapes refuse. Not every upstream model-effort/default/modality mapping has complete parity. Filesystem recovery preserves conflicts; fixture tests do not establish safety against every external native writer race.

Optional first-party TLS and experimental Desktop behavior have separate consent and cleanup requirements. See [native compatibility](native-compatibility.md).

## Cursor Private Inference: manual gateway setup

Cursor Private Inference is a separate local-agent build of Cursor. This guide follows the pinned compatibility reference, which inspected build 3.18.25; it is not a claim that a current download is available or that every later build behaves the same way. Cursor sign-in is still required. Obtain a suitable build yourself through a trusted Cursor distribution channel. TaiRouter does not download or install it.

Regular Cursor's custom endpoint requests can originate from Cursor's servers, so a loopback address on your computer is not reachable through that workflow. Confirm that you are using the Private Inference build before selecting the loopback option below. Do not modify the application to imitate that build.

This integration is manual. There is no `tairouter configure --client cursor` command, automatic settings writer, installation detector, installer lookup, or Cursor-specific status card supplied by this guide. Make changes inside Cursor; do not edit its settings database or credential-store entries. Keep any existing gateway values you need to restore, without exposing their secrets.

### Enter the URL and key

1. In TaiRouter, select the intended organization, connect your own providers, and configure the model routes you want to use.
2. Issue a personal inference key in that organization for direct HTTPS access. Record it securely when shown.
3. In Cursor Private Inference, open **Settings → Models → Gateway**, then **Configure gateway**.
4. Enter your server's public origin followed by `/v1`, for example `https://router.example.com/v1`. The hostname is a placeholder; use the actual operator-provided HTTPS address.
5. Enter the issued TaiRouter inference key in **API Key**. This is an TaiRouter key, not a provider credential or OIDC login token. A placeholder is not valid for the remote service. Saving it here is an intentional additional copy in Cursor's own credential storage.
6. Select **Refresh model list** and enable the returned model rows you want. Use the exact IDs returned for your organization/user routes; do not assume upstream example IDs exist in your account.

Alternatively, use an existing named TaiRouter connection profile and its HTTP loopback relay on the same computer. Start it using the [relay commands above](#profiles-and-relay-trust). For the default relay port, enter `http://127.0.0.1:17843/v1`; substitute the profile's actual fixed port if different. The relay supplies its profile credential, so Cursor's required API Key field can contain a non-secret placeholder for this option. Do not paste the remote key into that field unnecessarily. This placeholder works only with the local relay, never with the direct HTTPS server.

The relay has no separate local authentication, so other local processes share its access. Keep it on loopback and use it only under that computer trust policy. Stop it when no longer needed. This manual gateway setup does not require optional native TLS compatibility, certificate enrollment, or a public tunnel.

### Models, reasoning, and context

Refreshing the list requests `/v1/models`. A populated picker establishes model-list access, not successful inference, account entitlement, or complete protocol compatibility. Run a non-sensitive test with a selected route and inspect the resulting usage/error metadata. Cursor's local-agent workflow can add a substantial system prompt; allow for its token usage and the target model's context limit.

| Control      | What determines it                                                                                               |
| ------------ | ---------------------------------------------------------------------------------------------------------------- |
| Model picker | The gateway's authorized model list; refresh after changing routes or credentials                                |
| Reasoning    | Advertised capabilities plus the installed Cursor build's own model-family table; some model IDs have no control |
| Context      | Available model/context metadata and Cursor's UI rules; do not assume an enlarged context window is available    |

A context-window or Max Mode choice is different from a reasoning-effort level. Do not infer `max` or `ultra` support from a label alone. The pinned client can choose a wire protocol from advertised API capabilities, and effort controls may not map identically across Responses and Anthropic Messages. A visible control does not prove that the selected provider received that effort. This guide does not promise upstream effort-row settings, a bundle-derived reasoning table, or an installed-client prediction UI in TaiRouter.

The pinned Private Inference build uses the gateway's model list instead of the regular Cursor catalog; regular-client features such as Tab completion and Cloud Agents must not be assumed available. Configuring a Cursor provider connection in TaiRouter is a separate operation: it supplies an upstream account for routing and does not configure this desktop client. Neither operation authorizes TaiRouter to run local Cursor programs, shell commands, or filesystem tools as a provider backend.

### Check and undo setup

- If authentication fails on the direct server, check the key's organization, expiry, revocation, and membership. Do not replace it with a placeholder.
- If the picker is empty, check the URL's `/v1` suffix, relay/server availability, and eligible routes, then refresh the model list. Cached client lists can require an app restart.
- If reasoning/context controls are absent, check the installed client's capabilities and returned model metadata; do not invent model aliases or advertised capabilities to force a control.
- To undo the manual setup, restore or clear the gateway values inside Cursor and revoke the dedicated TaiRouter key. For the relay option, stop/remove the relay and disconnect its profile when no longer needed. TaiRouter has no Cursor settings backup to restore automatically.

No live Cursor sign-in, installed-app, platform, or provider acceptance is claimed. Cursor platform/build availability is separate from TaiRouter's CLI targets; in particular, the TaiRouter CLI does not target Windows ARM64.

## Codex context management

Context history and notes require explicit opt-in for your connected profile. They use your personal OpenAI OAuth account; API keys for other providers cannot substitute.

1. Run `tairouter context --profile default --action enable` and confirm the external-storage notice.
2. Run `tairouter configure --profile default --client codex --context-management`. Review and confirm the settings preview. Existing backup, consent and restoration rules still apply.
3. Make a successful Responses request with the same session ID that the client sends in `session-id` or `thread-id`. The router records that session's exact account; it never picks a new account for existing history.
4. Run `tairouter context --profile default --action bind --session YOUR_SESSION_ID` and confirm registration. Unobserved or ambiguous sessions are refused.

Bindings expire after 24 hours and are restricted to the original organization, user and profile key. Credential replacement requires a new observed session. `tairouter context --profile default --action disable` removes the key's bindings. Provider history and notes may remain stored at the external provider; disabling the router capability does not delete them. These source workflows do not establish live provider or native-client acceptance.
