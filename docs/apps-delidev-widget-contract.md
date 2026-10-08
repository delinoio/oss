# DeliDev macOS status widget

## Scope

Issue #1090 adds the initial read-only macOS WidgetKit extension under
`apps/delidev/macos-widget`, the desktop's `widget_host` presentation adapter,
and native preparation/verification scripts. Other OS widgets, production
signing/publication and continuous background-refresh guarantees are excluded.
Issue #1410 moves persistence and shutdown joins off the native UI loop without
changing widget refresh scheduling or the protected Swift storage format.

## Runtime and Language

Swift/Foundation owns protected local snapshot persistence and SwiftUI/WidgetKit
presentation. A bounded in-process C ABI connects the existing Rust desktop host
to the snapshot writer. Both extensions target macOS 13, with native arm64/x64
builds. Go remains the business-logic owner; the widget has no Go business logic,
networking client, account storage, server supervision or agent execution.

## Users and Operators

Each widget instance explicitly selects one previously observed saved server.
Open that server's desktop window to refresh its metadata. An unset, removed or
unavailable selection never falls back to another server or the local owner.
Closing a window or quitting the app does not start background product work.

## Interfaces and Contracts

The WidgetKit bundle is `io.delino.delidev.widget`; the Intents selection extension
is `io.delino.delidev.widget.selection`. Both and the containing application use
App Group `group.io.delino.delidev`. `SelectServerIntent` dynamically enumerates
only saved-profile UUID-v7 identities in the protected snapshot; it has no default
server and no execution action. Widget clicks only have the system's ordinary
containing-app activation, with no product mutation or action URL.

The desktop reuses its direct authenticated Connect Query reads for overview,
UTC-day usage and bounded account quota metadata. Native saved-window ownership,
fresh presentation scope and increasing revision checks precede publication.
Only the native binding supplies the saved profile ID/name; the renderer cannot
choose a profile, directory or file. Local product windows are excluded. Within one process, only the oldest ready
window for a saved profile publishes that profile's Widget snapshot. Closing it
hands publication to the next ready window without creating another record,
changing profile identity or manufacturing a refresh timestamp. Tray publication
remains independent for every window. A process-owned FIFO persistence worker
orders snapshots and removals independently of tray rendering. The tray lock
orders admission only; no tray, binding, registry or queue lock spans persistence.
Before each queued snapshot reaches storage, recheck its original window instance,
presentation scope/revision and current oldest-ready owner, and read the latest
committed native name. Retain earlier admitted revisions within the same scope
in FIFO order, so a later unavailable refresh preserves their last successful
observation. Replaced scopes and revoked writer ownership discard queued
predecessor snapshots. An already
executing write finishes before any successor can write, so it cannot overwrite
a newer writer's persisted snapshot.

The queue holds at most 64 pending projections. Admission never waits for storage
or queue capacity; a rejected refresh retains the accepted in-memory tray state
and lets the persisted observation expire. Native rendering skips contended state
instead of waiting. Quit closes publication admission immediately on the native
UI loop. Its existing tracked shutdown worker joins the tray timer and drains
admitted widget operations, then the persistence worker publishes one final stale
snapshot and exits. Native exit remains pending until that join completes, even
when storage stalls. Repeated Quit shares the same operation, and no snapshot
can become fresh after its final stale publication. A failed final write reports
storage uncertainty; it does not fabricate durable stale state or retry blindly.

Snapshot publication keeps exact integer token strings, known zero, missing
observations and explicitly incomplete coverage. Optional historical token-price
estimates keep each original currency and decimal spelling independently; they
are neither summed nor converted through floating point. Actual cost remains
unavailable. This adds presentation fields to the local tray projection only;
the public RPC/CLI behavior and protocol schema remain unchanged.

Quota windows remain separate, with their original state, basis-point precision,
observation/reset times and pagination/truncation indicators. Missing quota
observations remain unavailable. A passed reset or expired observation marks
the quota stale without inventing recovery or pooled capacity. Aliases and saved
server names are masked for email/recognized credential syntax and stripped of
controls at the persistence boundary as well as the existing tray boundary.
Recognized syntax is case-insensitive `bearer `, `sk-`, `ghp_`, `github_pat_`,
`token=`, `password` and `api_key`, plus email-shaped values containing `@`.
Account aliases are masked before renderer-to-native tray IPC and account/saved
server names are masked again before native menu rendering. Stored labels and
original resource/navigation identities remain unchanged.

Successful refresh time comes from the validated server overview, never from
disk write time, a reload request or a widget timeline. Failed refresh retains
the last successful summary/time under stale state; an unobserved server has no
successful timestamp or fabricated counters. Freshness expires after 45 seconds,
including future-dated observations after a clock correction. Explicit desktop
exit marks snapshots stale without replacing their successful timestamps.
Abrupt termination naturally expires the last observation.
The view uses the shared pure presentation-time boundary: validate each successful
overview and quota observation against the actual wall clock before allowing the
scheduled timeline date to force expiry. A future timeline date cannot mask a
backward clock correction or change the retained successful timestamp.

Timelines schedule the known stale transition and request another local read
after five minutes. `WidgetCenter.reloadTimelines` follows publication; both
reload and timeline delivery are OS best effort and cannot establish live data.
Small/medium/large views use bounded presentation and disclose additional records
in DeliDev rather than treating a partial view as complete inventory.


### Widget language

English/Korean presentation follows [the localization contract](apps-delidev-localization-contract.md). The independent device Language controller and protected preference stay above connection and Settings visit ownership. Preserve stable category/enum/RPC values, drafts, focus, exact operation identities and original technical evidence. Native/widget catalogs generate typed resources during preparation, tests and packaging; widget language publication never advances server observation timestamps. App body language follows the saved device choice; OS-owned standard UI and widget gallery/selection guidance follow native localization. Fixture/build/package results remain distinct from actual platform and provisioned WidgetKit acceptance.

## Storage

The OS entitlement API resolves the App Group, never a renderer-selected path.
The writer owns an owner-private `StatusWidget` directory (0700), snapshot and
lock files (0600). Version 1 stores at most 32 distinct saved-server records in
one bounded 2 MiB snapshot file. Each publication is at most 128 KiB; account
entries/windows and estimate currencies retain their independent bounded limits.

Open files without following links, reject foreign ownership, shared modes,
nonregular or multiply linked files, and fail on malformed/oversized snapshots
without resetting them. A nonblocking OS writer lock serializes processes;
private synchronized staging and atomic rename publish the whole snapshot.
Readers observe the previous or complete replacement. No account tokens,
endpoints, credentials, prompts, conversation text or execution paths are copied.
Ordinary window destruction retains stale-able metadata; confirmed profile
removal deletes only its own snapshot entry. Presentation-storage failure cannot
undo authoritative credential/profile removal and emits a closed diagnostic.

## Security

Both extensions are sandboxed with only the App Group entitlement, no network
client/server or Keychain entitlement. The containing CEF app remains outside
App Sandbox and retains only its three pinned bundler-owned CEF execution
entitlements alongside the group. Neither extension may inherit those execution
entitlements. A widget-storage failure cannot disable the existing tray; it emits
its own closed diagnostic while the persisted observation expires normally.
The persisted projection has no authentication or product authority.
The writer reconstructs metadata from validated typed fields before serialization;
extra credential/content fields are never copied. Selection is exact by profile
identity and cannot select arbitrary paths or silently switch servers.

## Logging

Rust emits structured `widget_snapshot` admission, write, final-stale and join
phases with closed native failure codes. Swift returns a closed ABI result, never an
OS error description. Exclude group paths, aliases, token/cost totals, quota
amounts, secret values and raw snapshot bytes from operational logs.

## Build and Test

`pnpm --dir apps/delidev prepare:widget` builds both extensions for the native
macOS architecture under ignored `target/delidev-widget`; other OS hosts skip it.
Desktop development and ordinary/dry-run packaging explicitly prepare these
extensions before embedding them under `Contents/PlugIns`. The containing app
declares its group entitlement; extensions retain their own sandbox entitlements.
Build children inherit only the existing credential-free native tool environment.
Local Xcode compilation disables provisioning; subsequent ad-hoc signing verifies
structure and signatures, not production identity or provisioned App Group access.

`pnpm --dir apps/delidev test:widget` compiles/runs native Swift fixtures using
isolated temporary state only. It is included in the frontend `pnpm test` command;
non-macOS hosts explicitly report that native fixtures need macOS. Cover exact
large counters, zero/unknown, separate currencies, incomplete usage, stale/failed
refresh and app closure, quota expiry, two-server isolation/removal, masking,
corruption and owner-private file/link handling. Run root `cargo test` after Rust
changes, native host compilation, `pnpm ci:contracts` and `pnpm ci:workflows`.
Rust fixtures inject blocked and failed writers, prove that tray/window state
remains available during persistence, and cover pending Quit, final-stale order,
queue saturation, replacement scope/instance and oldest-ready writer handoff.
These controlled fixtures do not establish packaged native event-loop or platform
Quit acceptance; record those results separately in PRs/issues and CI artifacts.
Bundle verification must check identifiers, minimum OS, matching architectures,
both embedded extensions and their exact entitlements/signatures.

## Dependencies and Integrations

The existing tray's authenticated read boundary remains authoritative. No new
RPC/CLI business operation or protocol/client generation is needed. Apple Intents
provides macOS 13-compatible instance-specific server selection. WidgetKit owns
refresh scheduling; see [Apple's timeline guidance](https://developer.apple.com/documentation/widgetkit/keeping-a-widget-up-to-date/).

## Change Triggers

Update this contract, desktop/packaging contracts, project index, scoped
`apps/delidev/AGENTS.md`, native tests and validation records in pull requests,
issues and CI logs/artifacts together when identity,
privacy, storage schema, selection, lifecycle, embedding or supported platforms
change. Record fixture/build results separately from signed installation,
Notification Center/widget-gallery interactions and real-server/provider evidence.

## References

- [Project](project-delidev.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Native package verification](apps-delidev-packaging-contract.md)
- [Requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)

### Quota reset countdowns (issue #1826)

The large widget's existing reset labels use localized elapsed-duration
countdowns calculated when an entry renders. Strict reset validation rejects
impossible wall dates; invalid, missing and expired values retain their existing
fallback. Use whole days/hours above one day, hours/minutes below one day,
minutes below one hour and Resets soon below one minute. Floor units, omit zero
trailing units and preserve bilingual plurals. Do not add labels to other sizes.

Retain the existing five-minute, OS-controlled best-effort timeline and original
successful-refresh timestamp. Countdown expiry cannot make observations fresh,
restore quota, initiate network/native operations or promise continuous updates.
Formatter/offscreen widget fixtures remain separate from installed WidgetKit
acceptance.
