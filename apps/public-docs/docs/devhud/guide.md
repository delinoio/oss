# Using DevHud

## First run and identity

DevHud opens in guest mode so you can inspect the shell and local preferences. Guests can preview and export diagnostics but cannot submit them. Sign in through Logto to synchronize supported account settings, connect GitHub, create issues, use official uploads, and recover an account. A custom API origin can provide authentication and settings for an installation, but official upload authority remains first-party.

Production authentication requires HTTPS, a trusted deployment, and the configured Logto issuer, client, audience, and callback. Local development may use the documented loopback setup; never send tokens or credentials in URLs.

## Settings and PAT profiles

Language, theme, Decks, URL mappings, agent descriptors, and upload-provider references synchronize for authenticated users. Device-only preferences include shortcuts, repository prompts, API-origin details, drafts, caches, permissions, Chrome pairing, and secure credentials. GitHub PAT profiles are explicit fine-grained or classic profiles: selecting a repository always selects a profile, and DevHud never silently falls back to another profile. PAT values remain in platform secure storage.

## Capture, drafts, and browser context

RealQA capture is desktop-only and uses still images. Screen-capture or X11 permissions may be required. Accessibility/Input Monitoring permissions may be required when configuring global shortcuts. Captures are encrypted drafts with a 30-day deadline renewed after a successful save. The default local draft quota is 10 GiB; unexpired drafts are not evicted. The editor accepts at most 2,048 Unicode scalar values and up to 10 images. Flattened PNG output is capped at 50 MiB per image; oversized output is proportionally downscaled and the editor reports that adjustment.

URL mappings associate a concrete browser origin and path with a repository and explicit PAT profile. The Chrome picker requests only configured-origin permission, redacts path segments and sensitive browser data, and sends one selected element after a user gesture. Pairing uses the desktop app's one-time flow; if it fails, select the repository manually.

## Images and submission

Official uploads use signed direct storage and publish opaque image URLs. BYO R2 is optional and uses a configured Cloudflare account, bucket, public base, and prefix; access keys stay device-local. Public images can be viewed by anyone with the URL. Treat issue content and image pixels as public; see [Privacy](privacy) for removal requests.

Direct GitHub submission uses the selected profile. The optional local-agent flow is off by default: agents are read-only and credential-free, Draft results require review, and Direct mode requires separate confirmation before DevHud writes. Never put secrets in prompts, issue text, repository instructions, or captured context.

## Decks and widgets

Deck polling targets 1, 5, 15, or 30 minutes while the app is active. GitHub rate limits, offline state, and blocked accounts stop or defer refresh and show a typed status. Notifications are local and opt-in. Widgets refresh on OS-controlled best-effort schedules, expose the selected Deck's private title, show stale state after 60 minutes, and retain the last successful results while suspended or offline.

## Offline, blocked, and account actions

Cached authenticated data and device-local settings remain useful offline where permitted; API writes remain pending or unavailable. A blocked or deletion-pending account cannot use authenticated actions or submit diagnostics. Logout is a local cleanup boundary and can remain retryable if cleanup is incomplete. Account deletion blocks access immediately and keeps encrypted unsubmitted drafts until each draft's existing 30-day deadline; deletion does not renew that deadline. Account data is then permanently purged. Restore is available only to the verified owner during the recovery window.

Diagnostics are off by default. Authenticated, unblocked users preview the exact redacted crash payload and consent per submission; guests can preview/export only. See [Privacy](privacy), [Security](security), and [Support](support).
