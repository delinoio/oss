# Using DevHud

## First run and identity

DevHud opens in guest mode so you can inspect the shell and local preferences. Guests can preview and export diagnostics but cannot submit them. You can configure GitHub and create issues directly as a guest with a device-local PAT; direct submissions can use BYO R2 or no images. Sign in through Logto to synchronize supported account settings, use official uploads, and recover an account. A custom API origin can provide authentication and settings for an installation, but official upload authority remains first-party.

Production authentication requires HTTPS, a trusted deployment, and the configured Logto issuer, client, audience, and callback. Never send tokens or credentials in URLs.

## Settings and PAT profiles

Language, theme, Decks, URL mappings, agent descriptors, and upload-provider references synchronize for authenticated users. Device-only preferences include shortcuts, repository prompts, API-origin details, drafts, caches, permissions, Chrome pairing, and secure credentials. GitHub PAT profiles are explicit fine-grained or classic profiles: selecting a repository always selects a profile, and DevHud never silently falls back to another profile. Fine-grained PATs require Metadata read, Pull requests read, Issues write, and Contents read access for the selected repository; classic PATs require the `repo` scope. PAT values remain in platform secure storage.

## Capture, drafts, and browser context

RealQA capture is desktop-only and uses still images. Screen-capture permissions may be required. On Ubuntu, global shortcuts require an X11 or XWayland session, a valid `DISPLAY`, and available user X authority; native Wayland is unsupported. Accessibility/Input Monitoring permissions may be required when configuring global shortcuts. Captures are encrypted drafts with a 30-day deadline renewed after a successful save. The default local draft quota is 10 GiB; unexpired drafts are not evicted. The editor accepts at most 2,048 Unicode scalar values and up to 10 images. Flattened PNG output is capped at 50 MiB per image; oversized output is proportionally downscaled and the editor reports that adjustment.

URL mappings associate a concrete browser origin and path with a repository and explicit PAT profile. Chrome's host permission grant covers the configured scheme and host across ports because browser permission patterns cannot encode ports; DevHud still enforces the exact configured origin, including its port, before injection. The picker redacts path segments and sensitive browser data and sends one selected element after a user gesture. Pairing uses the desktop app's one-time flow; if it fails, select the repository manually.

## Images and submission

Official uploads use signed direct storage and publish opaque image URLs. Each authenticated user may use up to 1 GiB in a rolling 24-hour period, store up to 20 GiB, and request up to 120 signed URLs in a rolling hour. Each signed PUT URL expires after 15 minutes, independently of the upload staging record, which expires after 24 hours. If an upload is paused or retried after the signed URL expires, restart the upload flow to obtain a new URL; the existing staging record does not renew that credential. Images are limited to 4096×4096 pixels and 16,777,216 total pixels, and public delivery is limited to 300 GETs per IP per minute. BYO R2 is optional and uses a configured Cloudflare account, bucket, public base, and prefix; access keys stay device-local. Public images can be viewed by anyone with the URL. Treat issue content and image pixels as public; see [Privacy](privacy) for current image-removal status and policy.

Direct GitHub submission uses the selected profile. Titles must contain 1–256 Unicode scalar values, and the complete composed issue body, including diagnostics and image Markdown, must not exceed 65,536 characters; DevHud rejects either limit before side effects. The optional local-agent flow is off by default and desktop-only. It accepts only preinstalled Codex `0.147.0`, Claude Code `2.1.233`, or OpenCode `1.18.18`; DevHud never installs or updates these agents. Managed local-agent clones can use up to 50 GiB of device-local storage; use the explicit cache-purge action in Settings to reclaim that space. Each local-agent run has a 15-minute deadline; when it expires, DevHud cancels the process tree and preserves the encrypted draft so you can retry or use the manual path. Agents are read-only and credential-free, Draft results require review, and Direct mode requires separate confirmation before DevHud writes. Never put secrets in prompts, issue text, repository instructions, or captured context.

## Decks and widgets

Deck polling targets 1, 5, 15, or 30 minutes while the app is active. An account can configure at most 25 Decks, and each Deck retains at most 100 results. GitHub rate limits, offline state, deletion cleanup, logout, and API-backed restrictions stop or defer refresh and show a typed status; administrative blocking alone does not stop local direct-GitHub Deck polling. Notifications are local and opt-in. Widgets refresh on OS-controlled best-effort schedules, expose the selected Deck's private title, show stale state after 60 minutes, and retain the last successful results while suspended or offline.

## Offline, blocked, and account actions

Cached authenticated data and device-local settings remain useful offline where permitted; API writes remain pending or unavailable. A blocked or deletion-pending account cannot use authenticated actions or submit diagnostics. Logout is a local cleanup boundary and can remain retryable if cleanup is incomplete. Account deletion blocks access immediately and keeps encrypted unsubmitted drafts until each draft's existing 30-day deadline; deletion does not renew that deadline. After the recovery window, synchronized data is purged. Upload metadata is purged or irreversibly pseudonymized only at the documented audit boundary; pseudonymized security and administrator audit records may be retained temporarily. Restore is available only to the verified owner during the recovery window.

Diagnostics are off by default. Authenticated, unblocked users preview the exact redacted crash payload and consent per submission; guests can preview/export only. See [Privacy](privacy), [Security](security), and [Support](support).
