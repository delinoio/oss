# DeliDev desktop localization

## Scope

`apps/delidev` owns English and Korean presentation for every desktop screen,
DeliDev-owned native menus, notifications and dialogs, and the macOS status widget.
The React catalogs live in `src/locales/{en,ko}`. Native/widget source catalogs
live in `locales/native`. CLI output, public documentation, conversations,
user-defined names, external service source text and technical identifiers retain
their original language and bytes.

## Runtime and Language

The existing React/TypeScript/Rsbuild application uses exact `i18next` 26.4.2 and
`react-i18next` 17.0.15 dependencies. Both languages are bundled; translation does
not load network resources or use Suspense. Rust owns the device preference and
native presentation. Swift/Foundation and WidgetKit own widget rendering.

## Users and Operators

Users select Follow system, English - English or Korean - 한국어 in
Settings → Appearance → Language. The system entry uses the current UI language.
The fresh-install default is System. The preference belongs to this computer,
independent of account, selected server, connection state and Settings visits.
Every authorized live app window updates immediately; newly opened windows and
restarts restore the committed preference.

## Interfaces and Contracts

The closed preference is `system | en | ko`; the resolved language is `en | ko`.
System scans the native preferred-language list in order, normalizes regional
variants and selects the first supported language. With none supported it selects
English. Reinspect at startup and retained-window activation, broadcasting a
changed resolved language without writing a new preference.

`LanguageProvider` remains above connection-owned state. A presentation update
never remounts a session, editor or confirmation, clears a draft or focus,
replaces a query client/request/job ID, retries a mutation, changes business
conditions or grants authority. Translated labels never serve as React keys,
persisted values, enum IDs, RPC arguments or query identities.

Native `read_language`, `update_language(language, expectedRevision)` and
`language-changed` use existing trusted-main and registered saved-window
URL/binding authorization. Snapshots contain increasing unsigned process
revision, preference, resolved language, closed problem and a separate optional
widget-storage problem. Old delivery cannot replace newer state; contradictory
equal revisions fail closed. A stale write requires inspection. Disk inspection,
atomic commit and native presentation publication retain serialized ordering.
No server API, protobuf field, DB migration or generic filesystem interface is
introduced.

Catalogs own buttons, help, validation, states, accessibility names, tooltips and
confirmation messages. Translate full sentences with named interpolation and
plural forms. Typed keys and catalog checks reject missing/empty translations,
variable/tag mismatches and incomplete plural families. Korean terminology is
Session → 세션, Agent Worker → 에이전트 워커, Runner Device → 실행 장치; canonical
English labels and all stable machine identifiers retain their existing values.
Dates and numbers follow the selected language while preserving timezones,
exclusive end boundaries, exact BigInt counts, independent currencies and every
decimal digit. Missing, uncertain, incomplete and stale observations stay distinct
from measured zero, success, actual cost and execution readiness.

Error codes select localized guidance. Original server message/guidance and
technical metadata remain inert text inside an explicit Technical details
disclosure. Existing native diagnostic redaction remains authoritative: protected
native content is not made available by localization. A disclosure contains only
original details already admitted by the owning diagnostic contract. A translation
never replaces original evidence or creates retry permission.

The Appearance category retains all 18 categories, theme choices,
colors, navigation and visit lifetime. A divider below Theme introduces Language.
Use an editable search combobox at most 320 CSS pixels wide and at least 40 pixels
tall, with 8-pixel corners and full available width below 640 pixels. Keep System
first, then sort languages by their stable English names, independently of UI
language. Show each language as “English name - native name”, including
“English - English” and “Korean - 한국어”. System is “Follow system” in English and
“시스템 설정 따르기” in Korean.

The closed input shows the committed label. Opening the input or its chevron
starts an empty search with the complete ordered list. Match English and native
name substrings after trimming and case folding; filtering retains the same
order. Typing highlights the first match but grants no save. Arrow navigation
stops at either end; click or Enter confirms one supported enum. Reselecting the
committed value performs no write. Escape, Tab, outside pointer interaction and
blur discard search without saving. Ignore confirmation/navigation keys during
IME composition, including legacy key code 229. An unmatched query shows a
localized status and cannot become a preference.

Keep DOM focus on the labeled input with combobox/listbox/option semantics,
`aria-autocomplete="list"`, expansion/control relationships and active-descendant
navigation. The list and chevron do not add Tab stops. Mark the committed option
with a decorative check and accessible saved-value description. Search help is
available to assistive technology. Place the list 4 pixels below the input in
document flow, at the same width, with 40-pixel minimum rows and a 280-pixel
scrolling cap. Use existing semantic theme colors and ordinary input boundaries;
the shared input focus treatment remains unchanged.

Search/open/highlight state belongs only to the mounted Appearance presentation.
A newer native revision closes the local search and displays the latest committed
label without remounting or taking focus. Reads and saves lock both input and
chevron and discard search. A pending save makes the input read-only and
ARIA-disabled to retain browser focus; reads and recovery failures disable it.
Korean scope help is “기본값은 시스템 언어입니다.
변경하면 모든 DeliDev 창에 바로 적용됩니다.” Selection auto-saves; there is no
Save button. Reading/saving locks selection and announces status. Failure retains
the last committed value and an explicit Reload action; uncertain writes never
auto-retry. Controls and text wrap in both themes, narrow layouts and 200% zoom.

Tray language refresh uses only retained observations and changes labels.
New notifications and native dialogs use the current language; delivered
notifications are never resent. OS-owned standard buttons follow OS localization.
Existing in-app toasts retain catalog descriptors alongside original string
content. A language update changes presentation while preserving the toast ID,
arrival order, remaining visible time, hover/focus pauses and close-button focus.
It never republishes a toast or replays the operation that created it.
User aliases retain separate masking provenance so a legitimate name matching a
mask placeholder is not translated.

Widget body language follows the protected App Group preference copy, including
unselected, offline, stale and unavailable states. Gallery and selection guidance
use native localized resources and the OS language. Language publication neither
reads/replaces the server snapshot nor advances any observation/success time.
WidgetKit timeline reload is best effort. Three sizes retain bounded truthful
presentation and disclose extra records in DeliDev.

## Storage

Native `app_config_dir()/language.json` contains only `{ "version": 1,
"language": "system" | "en" | "ko" }`, separate from `appearance.json`.
Bound reads to 4 KiB. Preserve malformed, linked, oversized or unsupported-version
files. Use owner-private same-directory exclusive staging, synchronized atomic
replacement, parent synchronization on Unix and write-through replacement on
Windows. A failed/uncertain outcome requires an explicit read before another
choice. Revisions are process-local and never wrap.

The widget stores the same version/preference in owner-private
`StatusWidget/language-v1.json` inside `group.io.delino.delidev`, separately from
snapshot data. Its existing protected open/owner/mode/link checks and nonblocking
writer lock apply; invalid documents are preserved. No server, account, endpoint,
credential, user content or raw preferred-language list is persisted. Language
preferences stay outside pairing, server backups and portable configuration.

## Security

Only trusted main and currently registered saved documents may read/write or
receive events. No external view or arbitrary path receives authority. Bundled
catalogs and interpolation are presentation data; dynamic user/native content is
never interpreted as another key, HTML or placeholder. Widget extensions retain
sandbox/App Group-only entitlements and no networking or Keychain access.

## Logging

Use structured stable language read/update/event and widget-presentation outcomes.
Do not log file paths, raw documents, preferred-language lists, server/user state,
aliases, native content or secrets. Widget persistence failure remains independent
of successful device-language commit and existing tray availability.

## Build and Test

`prepare:localization` regenerates TypeScript aggregation, typed Rust keys, Swift
catalogs and native `.strings` from reconciled sources. Preparation, frontend
build, widget build/tests and native packaging use it. `test:localization` checks
catalog parity, interpolation, plural families, source hardcoding exceptions and
reproducible outputs. Concurrent preparation skips unchanged output and publishes
each generated compiler input through a unique same-directory atomic replacement.
Compiler readers must observe complete old or complete new content; failed staging
is removed. Never hand-edit generated translations.

Run app-local `pnpm test`, root `cargo test`, native compilation, widget fixtures,
`pnpm ci:contracts`, `pnpm ci:workflows` and changed-dependency security checks.
Test System resolution, restart, new/multiple windows, delayed events, conflict,
failed/uncertain writes, malformed-file preservation and retained drafts/focus/
operations without added product RPCs. Inspect every screen and all 18 categories
in both languages; verify widget sizes, selection, offline/stale/storage failures.
`test-settings-layout.mjs` checks both catalogs across all existing categories,
forms, themes and effective 200% viewports, then primary surfaces and an Appearance
save transition. Host-supplied Playwright remains a validation-only tool.
`test:widget` includes protected-storage fixtures and offscreen SwiftUI layout
fixtures for both languages, three sizes and six states. The optional
`DELIDEV_WIDGET_LAYOUT_OUTPUT` and `DELIDEV_LAYOUT_SCREENSHOT` destinations contain
synthetic render output only; they never establish installed native acceptance.
Remove generated `dist` from the final worktree.

Record source revision, commands, results and unresolved limits in PRs/issues and
CI logs/artifacts. Fixtures, builds and ad-hoc packages cannot establish actual
macOS, Windows, Ubuntu X11, provisioned App Group, OS banner or WidgetKit acceptance.
No repository evidence document is added for this work.

## Dependencies and Integrations

The existing Connect/RPC clients retain business ownership. `sys-locale` supplies
native preferred languages. Existing Tauri CEF trust/binding checks, Appearance
storage rules, tray observations, Swift protected storage and WidgetKit scheduling
remain independent integration boundaries. Dependency changes follow the
repository dependency security contract.

## Change Triggers

Update this contract, desktop/widget/packaging contracts, project catalog and
scoped app/src/native/scripts AGENTS when languages, preference ownership,
storage, native interfaces, catalog generation or presentation boundaries change.
Validation-only updates belong in PRs/issues/CI and do not change ownership docs.

## References

- [DeliDev project](project-delidev.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Widget](apps-delidev-widget-contract.md)
- [Packaging](apps-delidev-packaging-contract.md)
- [Repository defaults](repository-defaults.md)
- [Dependency security](repository-dependency-security-contract.md)
- [i18next TypeScript](https://www.i18next.com/overview/typescript)
- [WidgetKit updates](https://developer.apple.com/documentation/widgetkit/keeping-a-widget-up-to-date/)

## Timestamp presentation

Issue #1736 adds the shared timestamp and device-date-format ownership described
in the desktop contract. English and Korean relative labels use floored elapsed
units below 24 hours. Computer-local absolute labels follow the selected closed
date preset; System follows the application language. Exact source timestamp
strings, offsets and fractions remain inspectable and never become machine
authority through localization. UTC backup and exact standalone PR-card labels
retain their explicit modes. Catalog interpolation uses inert React timestamp
slots so locale and display-clock updates do not stringify or remount them.

Issue #1826 adds complete localized quota-reset countdown sentences to the
React and native source catalogs. The existing generator produces Rust keys and
Swift catalogs. Days mean 24 elapsed hours. Floor day/hour, hour/minute or minute
units, omit a zero trailing unit and retain English singular/plural and Korean
unit order. Below one minute uses Resets soon / 곧 리셋. Ordinary timestamps,
source strings, device date preferences and successful observation times remain
unchanged. Expiry returns to the surface's original timestamp/fallback and cannot
prove quota recovery.

Settings search (issue #1869) indexes only bundled category/target label and static help keys in the current display language. NFC-normalized case-insensitive all-token matching retains visit-scoped query and focus while locale changes recompute results. No cross-language aliases, resource/private/native content, query persistence or logging is permitted. Localization cannot restart category/native controllers or repeat a consumed search focus target.

### Auxiliary tray status localization

Issue #1967 uses the separate `tray-status` bundled entry and English/Korean
catalog. Retained native snapshots project the committed resolved language,
semantic theme and date format without mounting writable device controllers.
Preference changes update the same panel and selection without a product read or
scope reset. Localize state, unavailable/empty distinctions, quota fallback IDs,
reset expiry, precision-preserving counts and existing navigation actions. Keep
explicit service brands and exact safe quota IDs separate from translated copy.
The auxiliary document has no preference-write or protected widget-store access.

## Keyboard shortcut preferences

The System Keyboard shortcuts category uses bundled English/Korean action names,
capture guidance, validation, explicit save/discard/default controls and native
preference problem classifications. Keep ASCII chord identities, action IDs and
revisions unchanged on locale changes. Re-render labels without resetting the
editor draft, capture opener, effective bindings or business request owners.
Browser-only persistence remains visibly unavailable. Follow the desktop
device-local shortcut preference contract; no captured content enters logs.
