# AIPRouter console design

This specification defines the approved console design alongside the [console behavior contract](apps-aiprouter-console-contract.md). Illustrative names, usage figures, addresses and connection states are fictitious. Visual examples do not establish runtime or accessibility acceptance.

## Surface and visual system

Multilingual responsive web console for personal AI provider configuration, organization management, and a separate instance operator workspace. Target desktop 1440×1000, tablet 768×1024, mobile 390×844; support 320 CSS pixels without page-level horizontal scrolling. Treat the boards as concept layouts, not pixel-exact viewport captures.

Use a calm, light, Toss-informed hierarchy: one main task per page, plain action labels, generous spacing and progressive disclosure. Page background #F7F8FA, white cards, primary text #191F28, secondary text #4E5968, blue #1D4ED8 actions, pale blue selected surfaces, red #B42318 destructive actions. Use system sans-serif, 16px body, 14px labels, 28–32px desktop headings and 24px mobile headings, line-height at least 1.5 for body text. Use an 8px spacing rhythm, 12px card corners, 8px control corners, 44px minimum primary control height, and explicit text labels for statuses. Validate actual rendered contrast before acceptance; the raster preview is not accessibility evidence.

## Language selection

The approved language selector and responsive behavior are specified in the [console localization contract](apps-aiprouter-console-contract.md#localization). Place this control above all console surfaces. Keep existing page hierarchy, permission boundaries and destructive-action semantics when localizing content.

## Navigation and ownership

Desktop: 232px sidebar, top organization switcher with role, personal navigation, conditional organization navigation, account menu at bottom. Main area has a single h1, scope subtitle, page action and content. Personal links: Overview, Providers, Routing, API keys, Usage, Connect a client. Organization section, available to owners/administrators: Members, Invitations, Organization usage, Audit, Settings. Account menu: Account, optional Instance console, Sign out. Operator entry is separate from organization roles.

The switcher shows organization name, current role, current selection, search and Create organization. Empty search results explain that no memberships match. Switching clears the prior organization's visible data, closes resource dialogs, updates the URL scope, then loads the selected scope. Confirm before abandoning unsaved form edits. Never copy providers, pools, routes or keys into the next organization. A persistent subtitle says “Your settings in Acme Studio”; provider and routing pages say “Only yours in Acme Studio.” A role badge is descriptive, never an authorization cache.

Mobile: 56px header with labeled menu button, brand and organization switcher. Navigation opens as a modal drawer with grouped links and current page. No reduced desktop sidebar or competing bottom navigation. Cards stack; tables become labeled record cards with accessible per-record action menus. Filters collapse into a Filters sheet with active-filter count and Clear filters. Wide charts have text summaries and equivalent data tables; layouts must not require drag gestures.

## Roles

Members manage only their own providers, accounts, pools, routing, API keys, usage and client connections within the selected organization. Owners and administrators retain exactly the same personal-data boundary. Organization usage exposes per-member usage, estimated cost, model and error metadata, never credentials, prompts or response bodies. Do not show an “inspect provider” link on member details.

Administrators can invite/remove ordinary members and edit organization settings. They cannot change the owner or other administrators. Owners can appoint/demote/remove administrators, transfer ownership and delete the organization. Hide unavailable actions and explain important blocked actions, such as a sole owner's attempt to leave. Recheck on mutation; if authority changes while open, dismiss privileged editors, remove unavailable navigation and show “Your permissions changed. This action is no longer available.” Preserve login after demotion.

Instance operators see only whole-instance health/aggregates, request limits, retention, approved destinations, common price overrides and instance audit. No organization/member drilldown, impersonation, suspension, provider configurations, or raw request data. Deployment configuration owns operator assignment; the console has no Add operator control.

## Sign-in and onboarding

Sign-in offers “Continue with your identity provider” only. Missing or unverified email produces an explicit blocking error and a retry path after correction at the identity provider. Do not offer password signup or merge-by-email. Session expiry asks the user to sign in again and never automatically submits a pending mutation. Logout invalidates the session.

No-membership landing page: “Choose your organization,” pending invitation cards, and Create organization. A single organization-name field receives focus when the create form opens. Do not auto-create a personal organization. Invitation detail identifies the organization, intended email, role and expiry. Wrong verified email shows “Sign in with the invited email address”; expired, canceled, already-used and superseded links have distinct outcomes.

Overview includes a five-step checklist: Choose organization → Connect personal providers → Configure models/routing → Create API key → Connect a client. Completed steps remain navigable. Do not mark a step complete merely because a dialog opened. The guide is recoverable from Overview, not a blocking tour. Returning users see connection health and personal usage above the checklist.

## Providers and routing

Provider list: provider name, personal account label, authentication method, last verified status and actions. Statuses: Not connected, Connecting, Connected, Reconnect required, Cooling down, Connection failed and Unknown status. Status cannot be conveyed by color alone. Empty state: “Connect your first provider” with a clear personal-ownership explanation. Connect flow chooses a supported provider and supported authentication method. API-key inputs are masked and have explicit reveal controls; OAuth shows redirect/pending/canceled/failed states. Never redisplay stored secrets. Replacement is a new secret entry. Provider support must follow the [compatibility inventory](servers-aiprouter-compatibility-contract.md), not the illustrative cards.

Pools live under Providers and contain only the user's accounts in this organization. Routing lists model aliases and ordered primary/fallback connections. Editor offers only this user's eligible accounts/pools, shows the configured order and unavailable dependencies, validates duplicate names and missing connections, and requires explicit Save. Explain provider-specific policies through contextual help; do not suggest a universal retry count. Private/local HTTP destinations show an operator-policy error when disallowed. There is no local executable backend, workspace selector or execution permission UI.

Disconnect confirmation names the account, affected pools/routes and future-request effect. Do not silently remove dependent routing or imply external provider-account deletion. If a dependency requires repair, offer a route link. Connection failures preserve safe form values, but secrets must not enter URLs, diagnostics or notifications.

## API keys and client connection

Key list: name, masked identifier, ordinary/CLI type, created date, expiry or “No expiry,” status and Revoke. Create form asks for name and expiry, with an explicit no-expiry option.

Create API key dialog: a single-column dialog with a 560px maximum width and 32px padding on desktop and tablet (16px padding and 16px side margins at 480px and below). It uses the scoped `create-key-dialog` class on the shared `Dialog`, so other dialogs keep their sizing. Order: heading, “Close dialog” button, “Use a name you can recognize later.”, Name (empty, autofocused, required, 100-character maximum, helper “Up to 100 characters.”), Expiry (7 days, 30 days, 90 days, No expiry; 30 days initially; helper “Choose 7, 30 or 90 days, or No expiry.”), “You can revoke this key from API keys.” as plain guidance, the inline error area, a polite status line, then a divider and footer with Cancel followed by Create API key. At 480px and below the footer buttons stack full width in the same DOM order, and the dialog scrolls inside its bounded height with no page-level horizontal overflow. Controls and the Close button are at least 44px tall. Helper and error text are associated with their control through `aria-describedby` and never replace an existing description (`Field` merges ids and accepts an extra `describedBy`).

Submission is unchanged: the name is trimmed, the current organization id is sent, `expiresAt` is computed at submission time as `Date.now() + days * 86400000` and omitted for No expiry, and a whitespace-only name keeps the primary disabled. Opening the dialog or changing a field never creates a key; click and Enter each submit once through the shared pending guard. While a submission from this dialog is pending, the primary shows a disabled “Creating…” label and an in-dialog polite status says “Creating… Closing this dialog does not cancel a submitted change.” Cancel, Close and Escape stay available and dismiss without claiming cancellation; a confirmed success still closes the form and opens the one-time result, even if the form was dismissed while pending. A recoverable failure keeps the name and expiry, re-enables the valid submit and shows the sanitized error (with safe request ID and retry-after guidance) before the footer, announced once as an alert; there is no early success or automatic retry. Pending or failed state from an earlier action (for example a failed revoke) is never shown in a newly opened create dialog. A new session, organization or role remounts the page, so the next dialog starts with an empty Name and 30 days. The raw key never appears in this form, storage, logs or URLs. One-time result shows a copyable raw key, warning “This key is shown only once,” and Done. Do not persist the raw value in browser storage or subsequent list views. Copy confirmation is announced accessibly. Leaving the result never suggests that the same secret can be recovered.

Revocation dialog identifies the exact key and organization: “New requests stop immediately. Responses already sent to a provider may finish.” Confirm with “Revoke key”; focus Cancel initially. Expired/revoked records cannot be reactivated. Distinguish failed revocation from successful revocation. The UI must not report success before server confirmation.

Connect a client provides the selected server and organization plus instructions from the verified CLI interface; this proposal does not invent CLI commands or platform support. Explain that the CLI previews settings changes and asks before applying them. Separate confirmation is required if a client stores the remote key in its settings. Show actionable help for OS credential-store failure, outstanding key revocation and restoration conflicts; never offer a plaintext credential fallback. Restoration-conflict guidance preserves settings/backups and explains manual recovery separately from server revocation status.

## Device approval

The browser approval page is a focused layout, accessible independently from the console sidebar. If needed, a single code input receives focus. Show server address, device label, original one-time code, organization selector, key expiry selector including No expiry, and time remaining from the original ten-minute deadline. Copy: “Confirm this code matches your CLI. Approve only a connection you started.” Approve connection and Deny are explicit actions; opening a URL or signing in never approves automatically.

States: sign-in required, code entry, invalid code, pending approval, submitting, approved/waiting for CLI storage, connected after confirmed handoff, denied, expired, and failed delivery. Do not show the raw CLI key in the browser. An approval success message says “Return to your CLI to finish”; it must not claim local storage succeeded. Polling/retries do not extend the original deadline. Unclaimed expiry explains that the issued key was revoked. On local storage failure, direct the user to API keys if compensating revocation remains incomplete. The browser does not purport to inspect the OS credential store.

## Usage and estimated cost

Personal usage defaults to Last 7 days, with date range, model and outcome filters. Show request count, confirmed tokens when available, known estimated cost, and counts missing usage/prices. Copy example: “Known estimated cost $4.82 · 3 requests with unknown cost.” Missing values say “Unknown,” not 0 or an unexplained dash. Charts show gaps for unknown data and include textual/table equivalents. Clearly label currency and the display timezone. This is estimation, not billing.

Request metadata rows include time, model, outcome, confirmed usage, estimated cost and safe request ID; no prompts or response bodies. Detail explains price provenance and that the applied estimate is fixed for that request. A price update never changes historical display. Differentiate upstream error, gateway error, cancellation, rate limit, unknown usage and usage-recording failure. Do not infer complete totals when records could not be written. Organization usage adds a member filter only for owners/administrators. No export button.

## Members, invitations, transfer and audit

Members show identity, role, joined date and permitted actions. Search and pagination work on desktop and mobile. Remove-member confirmation explains immediate key invalidation and deletion of that membership's personal provider/configuration/temporary state, with retained usage subject to retention. A user rejoining must configure personal resources again.

Invitations show invited email, role, delivery status, created/expiry dates and permitted Resend/Cancel actions. Distinct statuses: Pending delivery, Retry scheduled, Rate limited, Sent, Delivery failed, Accepted, Expired, Canceled and Superseded. Resend has a visible cooldown and accessible explanation: minimum one minute, organization cap of 100 send attempts/hour. Show server-provided next-attempt time when deferred. Final delivery failure says the link is invalid; manual resend creates a new seven-day invitation and invalidates the previous link. Never label an SMTP delivery attempt as acceptance.

Owner nominates an existing member for transfer. Show one pending transfer with nominee, expiry and Cancel transfer. Replacing it explains that the previous request becomes invalid. Nominee acceptance page names the organization and resulting roles: nominee becomes owner; previous owner becomes administrator. Seven-day expiry and membership/authority failures are explicit. The current owner retains ownership until acceptance. There is no owner-role dropdown that transfers immediately.

Organization audit shows timestamp, actor, action, safe target and outcome. Filter by action/actor/time. Include permission changes, invitation lifecycle, key issuance/revocation and provider-connection changes. Do not embed secret values or content. Instance audit is in the operator workspace only and includes instance-setting changes. Anonymized actors remain identifiable as “Deleted user,” not a guessed identity.

## Operator workspace

Separate “Instance console” header with “Back to organizations.” Navigation: Health, Request limits, Retention, Destinations, Price overrides, Audit. Banner: “Whole-instance health and aggregates only.” Health shows whole-instance availability, request/error aggregates and cleanup/recording health with Unknown/Unavailable states; no clickable organization breakdown.

Request limits: default 120 requests/minute and 10 concurrent requests, applied per user within each organization. Range validation: 1–6,000 and 1–100 respectively. No unlimited option or instance-wide concurrency input. Retention: request metadata 30 days (1–90), daily aggregates 365 days (30–730), audit 90 days (30–365). Reducing retention requires a consequence review before Save. Destination approval identifies the precise destination and affected policy; no provider-secret configuration. Price overrides show model, units, currency and provenance, explain “Applies to new requests only,” and preserve historical estimates. Changes show validation, pending, success and failure states without erasing entered safe values.

## Destructive and account flows

Use named-action dialogs, never generic “Are you sure?” dialogs. Cancel is visually neutral; irreversible actions use red. Esc, close button and Cancel dismiss before submission. During an in-flight mutation, dismissal does not falsely imply rollback; report the final result in the page after dismissal. Prevent duplicate submission.

Delete organization is owner-only. Require exact organization-name confirmation; disable submission until the entered value matches. The board illustrates button placement and destructive color, not its disabled styling. Explain immediate deletion of credentials, settings, temporary state, usage and organization audits; new requests stop, already-dispatched responses may finish. Explain existing backups follow operator policy. Do not promise instantaneous backup erasure. Leave organization uses a separate confirmation with the narrower membership consequences. Block leaving as sole owner with Transfer ownership and Delete organization alternatives.

Account deletion first lists owned organizations that must be transferred or deleted. Once clear, explain removal of memberships and personal identity, anonymization of surviving usage/audit records, and operator-controlled backup expiry. Require an explicit final “Delete my account” confirmation. Do not offer deletion as an error recovery action.

## Shared states and accessibility

Every data page has a skeleton with an accessible loading message; first-use empty state with one primary action; no-results state with Clear filters; retryable error with safe request ID; permission-denied state; and stale/offline state that never shows unconfirmed mutation success. DB unavailability must not imply cached authorization permits actions. Changing organizations or losing membership must clear previously scoped content immediately. Preserve only safe unsaved data; do not retain secrets after session/scope loss.

Target WCAG 2.2 AA: landmarks, skip link, ordered headings, real links/buttons, associated labels, error summary plus field errors, visible focus, keyboard access, accessible name for every icon, and status text independent of color. Design target 44×44px touch controls, at least AA minimum targets. Check 4.5:1 normal text and 3:1 large text/non-text control contrast. Focus must not be obscured by sticky headers or action bars. At 200% zoom and 320 CSS pixels, reflow content without losing actions. Respect reduced motion.

Dialogs trap focus, have a named heading, support Esc and return focus to their trigger. A single critical input receives focus when its form appears, including organization name, invitation email, code entry and typed deletion confirmation. Otherwise focus the heading or safest action. Use polite live regions for copy/save completion; assertive announcements for blocking errors only. Never announce a countdown every second. Announce meaningful expiry thresholds once. Do not rely on hover for help or menus.

## Review and acceptance scenarios

Review desktop/mobile scope labels, navigation, provider cards, routing, key creation/revocation, usage unknowns, invitations, member permissions, operator settings, onboarding and device approval against this text. Verify keyboard-only switching/navigation, focus entry/return/Esc, single-input autofocus, screen-reader names/statuses, zoom/reflow, contrast and reduced motion in the eventual implementation.

Exercise same-user organization switching with different credentials; admin demotion while editing; owner transfer replacement/cancellation/expiry; invite wrong-email/failure/resend/cooldown; key shown-once/revocation; device denial/expiry/interrupted handoff; unknown usage/cost and fixed historical estimates; deletion with in-flight requests; account deletion blocked by ownership; storage failure and restoration conflict. Confirm operators cannot inspect an organization or member's private data.

Product exclusions remain defined in the [project index](project-aiprouter.md). This design does not establish provider compatibility, validated accessibility, runtime acceptance, native acceptance, or deployment readiness. Record implementation and validation evidence in pull requests and CI artifacts.
