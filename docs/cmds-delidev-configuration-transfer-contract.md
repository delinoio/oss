# DeliDev Portable Configuration

## API format generation exclusion

The prior portable v4 shape retained selected API protocols and Provider profiles.
The current version-7 shape preserves those fields and rejects older imports. Capability-9 current/retained connection generations,
`credential_id`, health/validation/catalog and protected credentials are excluded.
Imported API accounts always start disconnected and cannot import old execution
authority. See the [account contract](cmds-delidev-accounts-contract.md#connected-api-format-changes).

## API format bundle version 4

Version 4 preserves Provider `api_formats` and Account `api_protocol` under the
[catalog contract](cmds-delidev-catalog-contract.md#api-account-format-selection).
Current exports and reviewed import plans use version 7. Versions 1–6 are
unsupported under the current-only reset; older files remain untouched. The new
shape preserves typed harness inheritance/overrides instead of reading omitted
fields as old explicit selections. Profile declarations
and account selections use the existing complete relationship validation and
atomic publication. Accounts start disconnected; connection pins, credential
references, keyless cleanup proof state, validation/catalog/quota observations,
routing state and session/Usage history are never exported. Existing target
accounts cannot be reused or reconnected by import. No SQLite migration is added.

## Scope

`cmds/delidev-cli` owns portable configuration validation, consistent export, change previews and atomic import. `apps/delidev` presents these owner/client operations through generated Connect Query bindings. Version 2 covers the eight existing editable configuration kinds: providers, models, account preferences, instruction templates, Agent Workers, repositories, projects and server preferences. Schedules, historical pricing versions, device-specific notification preferences, integration authentication and Worker installation settings are separate contracts, not silently included in this format. This increment does not complete the remaining the feature requirements.

## Runtime and Language

Go owns all validation, relationship remapping, authentication and SQLite transactions. React retains exact original JSON bytes for export, preview and apply; display parsing must never round-trip integer values through JavaScript numbers into authoritative documents.

## Users and Operators

An owner or paired desktop client can move supported non-secret configuration between explicitly selected servers. Import creates independent accounts requiring a new explicit connection and validation. It never reuses an account by alias, source UUID, provider name or available credentials. Workers cannot export, preview or apply owner configuration.

## Interfaces and Contracts

`ConfigurationService.ExportConfiguration` returns one consistent version-7 bundle with ordered entries and descriptive machine references. Export excludes authentication material, account connections/health/quota/catalog observations, model discovery provenance, device registrations, native installations, routing state and session/history contents. Provider-observed model metadata becomes user-declared advisory information; imported models are manual registrations. Instruction text, ordering, permissions, provider endpoints, project restrictions and preferred repositories remain exact. Unsupported integration references reject the complete export instead of disappearing. Server remediation defaults and optional complete repository overrides are included. Their Agent Worker references are remapped as configuration identities, and their execution machines require explicit mappings even when absent from all checkout lists. Stable GitHub reviewer actor/App IDs and permission selectors remain exact external identities; no current access or execution grant is imported.

`PreviewConfigurationImport` takes the bundle plus explicit configuration bindings, machine bindings and checkout paths. Desktop device preferences may supply unique unconfirmed suggestions. Every row requires explicit confirmation before preview; preferences persist only the last confirmed accepted target device, never imported documents, source IDs, mappings, paths, confirmations or previews. Every referenced machine needs an explicit one-to-one mapping to a registered enabled target machine, even when its UUID matches. Every checkout needs an explicit target path, including when reusing a repository. Unused, duplicate, cross-kind, unknown and missing mappings fail. Source resource UUIDs are document-local references; newly created target UUIDs are fresh. Both current global identities and permanent deletion tombstones are rechecked before acceptance and deferred publication, so a reused/deleted target becomes a terminal preview conflict rather than an endlessly failing Worker report. Existing providers/models/templates/Agents/repositories/projects may be explicitly reused only when their complete portable values match after remapping. Account reuse is forbidden. Only server preferences can replace an existing entry, with its exact expected revision; no implicit singleton overwrite is permitted. Model identity/alias conflicts, broken dependencies and incompatible model/account/harness references reject the complete preview.

The preview runs in an authorized read transaction without receipt, event, filesystem inspection or configuration writes. Its complete before/after plan includes target UUIDs, intended actions, expected revisions and target machine descriptors. A 24-hour signed token binds the exact plan, server and authenticated actor. Altering the document requires a new preview. All relationships and relevant current revisions are checked again at application and before deferred publication. Preview and transactional publication share the direct-save new-provider-selection guard. Resolve dependencies against the complete staged import graph, but compare each target's original ID and expected revision against its pre-change record, never its staged replacement. New API account, inline Agent model/account and project Agent/account selections require enabled API providers. Unchanged historical references and exact explicit reuse remain compatible; subscription identities retain their independent authority. Current inline source keys are not saved Model UUIDs. A disabled provider or stale source detected after preview, including while repository inspections are pending, fails the whole import without partial configuration writes or automatic replacement. Preserve original provider/catalog ownership, accounts, request receipts and sanitized failure visibility.

`ApplyConfigurationImport` requires the reviewed preview and a UUID-v7 mutation identity. Exact receipt replay returns the original job's current outcome without another import, including after preview-token expiry. New attempts must still validate the signed token. If later configuration deletion redacts an affected receipt, its original digest remains and replay reports NotFound without recreation; the known coordinator job remains independently readable. Receipt data is reference-only; request digests bind the actor and exact reviewed document. Metadata-only logs contain operation/request/job identity, state and replay status, never configuration text or paths.

CLI:

- `configuration export [--output PATH]`
- `configuration preview --input PATH|- [--output PATH]`
- `configuration apply --input PATH|- [--request-id ID]`
- `job get --id ID` observes a pending validation job. Repeating the original apply request observes its current outcome through the durable receipt.

Without `--output`, export/preview use the ordinary versioned JSON result envelope. With `--output`, they create a new owner-only file containing the directly consumable document and synchronize it and its directory; they never overwrite a file or follow an existing link. A failed/uncertain write preserves the selected path for inspection. Configuration stdin cannot share stdin with an authentication token.

### cmds/delidev-cli/internal/worker constraints

- Portable configuration follows `cmds-delidev-configuration-transfer-contract.md`. Export only the closed supported configuration schema; never serialize protected/runtime authority. Import uses explicit machine/checkout remapping, fresh disconnected accounts, exact unchanged reuse or revision-bound singleton settings replacement, an actor/server-bound signed read-only preview, and atomic all-repository validation/publication. Preserve original instructions and native options; reject conflicts without partial writes. Terminal import jobs discard staged configuration copies, and exact reference-only receipt replay never creates another import.

## Storage

Imports with no new repositories publish all entries, metadata events, the completed coordinator job and its receipt in one transaction. Imports with new repositories first create only an `import-configuration` coordinator and ordinary read-only repository-inspection jobs. The server does not open Worker paths. Every selected checkout must report the exact canonical path shown in the preview; a differently resolved path requires a new explicit preview. Reuse of identical existing repository configuration does not claim a new live inspection.

All new repository inspections must succeed before any imported configuration is written. The same transaction checks current configuration, machine identity/enablement and the importing client's non-revoked authority and publishes the entire graph plus parent outcome. A failed inspection, Worker revocation/loss, stale settings, model collision or revoked importing client preserves existing configuration and marks the coordinator failed. Late reports cannot revive it. Unexpected storage failures roll back the child report and all tentative writes, preserving its exact retry authority. Existing accounts and sessions are never disconnected, resumed or dispatched by import.

Pending coordinators retain the non-secret reviewed plan needed to complete after restart. Terminal coordinators drop staged configuration and instruction copies, retaining only the version marker, metadata resource outcomes and sanitized failure. No schema migration is required; the existing durable job, receipt and metadata-event boundaries own the operation. Existing backup copies remain governed by the separate backup/deletion contract.

## Security

A bundle contains at most 256 configuration entries, 64 distinct referenced machines, 64 checkouts and 384 KiB of JSON. The machine count includes the complete union of checkout references, repository remediation overrides and server remediation defaults; repeated references count once. Export rejects an excessive count with the existing ResourceExhausted transfer error, without returning a partial document or changing source configuration. It never drops a policy or reference to fit the bound. Plans are limited to 768 KiB; complete RPC documents retain the existing 1 MiB domain limit. Relationship validation is bounded at 10,000 current configuration entries and 16 MiB, failing without partial results. Unknown schema versions, fields, enum values, credential-bearing extensions and injected server-owned observations are rejected. Portable templates are user-authored contents, not a mechanism for exporting a protected credential store.

Preview tokens grant only the exact reviewed import to the same currently authenticated actor. They are not credentials, device registration, account readiness, native capability or execution authority. Import must not invoke inference, provider discovery, login, Git fetch, checkout, filesystem repair or native installation.

## Desktop

Settings includes Import / Export, presented as a shared left-aligned 1040px maximum column with flat Export and Import sections and 720px form bounds for this feature, always-visible labeled file/JSON inputs and a noninteractive state-derived Load / Map / Review & apply indicator. Mapping, complete review, uncertain retry and result panels remain below the original inputs; the full scope/exclusion guidance and responsive presentation are owned by the desktop contract. Exports are selectable for copying; imports accept a bounded UTF-8 file or pasted JSON. The client displays source machines, requires explicit target selections and target checkout paths, and offers create/reuse choices plus explicit server-preference replacement. It displays the complete exact change document before the separate apply action. Editing the document or mappings invalidates the preview. Forms remain mounted across responsive changes, same-category reselection and same-identity reconnect within the active category, and pending requests retain their original wire bytes and request identity there. Category departure or leaving Settings discards the document, mappings, preview, client waits and retry presentation without implicit apply/replay or server job cancellation; fresh reads may observe already accepted server effects. An acknowledged malformed result remains blocked for inspection rather than permitting a replacement mutation. Job reads distinguish accepted/pending validation, completed application and unchanged-configuration failure. No Web Storage or persistent cache is used.

## Logging

Structured import logs contain the request UUID, coordinator job UUID, closed state and replay flag. Never log instruction/configuration documents, paths, provider responses or authentication material. Existing correlated sanitized RPC errors provide validation guidance.

## Build and Test

Run focused server and CLI tests with the race detector, complete DeliDev Go tests/vet, Buf lint and deterministic regeneration, and `pnpm test` in `apps/delidev`. Verify graph remapping, account isolation, exact instructions, all-repository atomicity, stale/revoked authorities, file no-overwrite behavior, signed-preview tampering, reference-only replay, and exact frontend bytes including integers outside JavaScript's safe range. Label real Worker/Git integration and actual native desktop interaction separately in pull requests, issues and CI logs/artifacts.

## Dependencies and Integrations

Uses existing configuration validation, SQLite transactions, durable Worker jobs, Connect authentication, signed cursor primitives and the private generated API client. No new listener or Tauri business binding is introduced.

## Change Triggers

Update this contract, project/catalog links and scoped AGENTS for new portable kinds, trust state, remapping rules, application semantics or bounds. Additional portable surfaces require explicit schemas and their own authentication/revision/activation semantics; never infer them from ordinary resource serialization.

Instruction-file updates in this requirement apply only to changes in development procedures, directory ownership or repository/domain development rules under the [instruction-update policy](README.md#instruction-update-policy); ordinary behavior and validation changes update the owning contracts and validation records.

## References

- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Requirements](cmds-delidev-requirements.md)
- [Configuration and storage](cmds-delidev-contract.md)
- [Accounts](cmds-delidev-accounts-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)

### Provider activation fields

Portable provider documents preserve optional `enabled` and `preset_id`. Missing activation uses the historical enabled default; update omission preserves stored false. Managed preset identity is validated against pinned server values. Six hosted presets exist by default on new and upgraded servers, so imports must explicitly reuse an equal target managed provider or reject its identity collision; collision checks occur both at preview and deferred apply. Imports never merge providers by name/URL or transfer account connection/readiness/key state. See [provider activation](cmds-delidev-provider-activation-contract.md).

## Service-native bundle version 2

Version 2 carries independent service-native accounts and models without Provider references. Import remaps their account/model UUIDs while preserving the closed service/harness identity; account reuse remains forbidden and every imported account is disconnected with no protected generation, native lease or quota observation. Export strips all managed subscription ownership extensions. Agents that still require legacy subscription reconfiguration reject export explicitly instead of exporting a dangling retired model or silently omitting the Agent.

API-only version-1 bundles remain importable and produce a version-2 reviewed plan. Any version-1 graph containing subscription accounts, native model source/service fields or native-subscription Providers rejects the entire preview before publication; no service is inferred. Version-2 relationship validation and model uniqueness compare the full source identity, retaining global CLI alias collision checks. Migration 28 owns legacy retirement independently of portable-format validation.

Remote repositories retain the required credential-free `remote_url` during export/import. Empty checkout lists require no machine/path mappings and finish the existing durable import atomically without inspection children. Optional folders retain every existing mapping and Worker check. Clients verify System capability 37 before sending repository import requests. Historical missing URLs are not inferred or converted; users must explicitly configure the source before a new save/import.

## Ordered account source routes

Portable version 3 retains every ordered Agent source route, model reference,
account reference, policy and weight. Remap all route references before complete
relationship validation and reuse comparisons. Export/import never transports
routing state, quota observations, connections or execution history. Version 1
API-only and version 2 service-native bundles remain accepted with their existing
limits; either rejects source-route Agents that require version 3. Before API-format version 4, current import
previews used version 3 and preserve their exact bytes through acceptance/publication.

## Inline Worker models and endpoint-only completion reservation

On complete current-only reset and inline-model activation, the reserved portable profile embeds exact route model IDs and required settings in Workers, with no independent Model entries or model UUID remapping. Keep atomic graph validation, explicit machine/source bindings and credential exclusion. Earlier portable formats are unsupported only after complete current-only reset activation. Until then, the active API-format version 4 retains independent Model entries and imports versions 1–3; this reservation cannot change those runtime semantics.

Follow the complete [catalog amendment](cmds-delidev-catalog-contract.md#inline-worker-models-and-endpoint-only-completion-reservation) and [current-only reset](cmds-delidev-structure-contract.md#pre-release-compatibility-reset). This reservation changes no runtime support or native/account acceptance.

## Project behavior settings

Follow [project behavior settings](cmds-delidev-catalog-contract.md#project-behavior-settings) for schema-2 documents, continuous inheritance, explicit original project context, policy precedence, first-publication durable plan decisions and portable version 5. Preserve the original domain authority and uncertainty rules; no SQLite migration is introduced.

## New-session defaults and branch prefix declarations

Follow [the feature](cmds-delidev-catalog-contract.md#new-session-defaults-and-literal-branch-prefixes) for System 56, Worker 38, schema-3 Project/Settings and portable version 6. Preserve capability 52, schema-1/2 reads and v1–5 imports; reject destructive old-client writes. Automatic creation mode yields only to an explicit checkbox edit. New immutable prefix declarations preserve original source revisions and legacy omission bytes. Shared Execute instructions remain literal, bounded, and absent in Plan/read-only Sidechat; existing native/account/Worker/workspace/recovery/cleanup ownership remains authoritative. No migration, native version gate, branch interception or automatic branch creation is introduced.

## Typed harness defaults in version 7

Follow [harness defaults](cmds-delidev-catalog-contract.md#harness-defaults-and-live-inheritance).
Current bundles and reviewed plans preserve schema-5 Agent selectors and schema-4
Project/Settings defaults. Remap Provider identities in every defaults scope along
with Agent source routes and account references. Exact profile protocol, source
family, mode, empty strings and zero remain unchanged. Agent imports require the
current typed marker; legacy Agent documents are not silently converted at import.
Reject unsupported versions, destructive Settings replacement and broken/removable
profile references before publication. Imported defaults never supply native
observations, execution provenance, credentials, account readiness or historical
snapshot authority. Atomic graph validation and explicit target mappings remain.

Earlier version-5/6 behavior and prefix paragraphs describe their original
amendments. Their earlier-format import statements are superseded by version 7;
no retired format is reactivated. Existing source portable files are never rewritten.

## Explicit Worker-local Codex imports

`PreviewNativeConfigurationImport` and `ApplyNativeConfigurationImport` use
System `NATIVE_CONFIGURATION_IMPORT_V1 = 85` and Worker
`NATIVE_CONFIGURATION_IMPORT_V1 = 57`. The server never opens a Worker path.
The CLI exposes `configuration native-preview --input PATH|- [--wait]` and
`configuration native-apply --preview-job ID --input PATH|- [--wait]`.
Both accept the existing global `--request-id`; uncertain retries must preserve
that identity and the exact original descriptor or selection.

The version-1 preview descriptor selects Codex, one current machine revision,
an ordered list of at most 16 explicit home/project directories, and exactly one
existing destination with its revision. The destination is `server-defaults`,
`project`, or `agent-worker`; server/project choices include a complete typed
harness source scope. Agent choices include the exact source scope and route
index. No route, ancestor, host home, native-selected profile, account or target
is inferred. One optional explicitly named profile is read after the base values
of each selected file. Each entry distinguishes the base layer from that explicit
profile in its provenance. Project scopes must form an explicitly selected ancestor
to descendant chain. A home scope, when selected, precedes project scopes.

The Worker reads only `config.toml` for a home or `.codex/config.toml` for a
project, plus `AGENTS.override.md` and `AGENTS.md` in each selected directory.
The first nonempty instruction candidate wins at each directory. Selected
instruction packages retain their complete UTF-8 bytes and native layer order.
Configured fallback instruction filenames, external instruction paths and other
native keys are unsupported; they cannot expand the selected read set. The
[native home instruction reader](https://github.com/openai/codex/blob/1f3f93473394b620b35580859b7e6864f7a9f948/codex-rs/codex-home/src/instructions/mod.rs)
and [project instruction reader](https://github.com/openai/codex/blob/1f3f93473394b620b35580859b7e6864f7a9f948/codex-rs/core/src/agents_md.rs)
define override precedence independently of DeliDev's explicit selection.

The reader rejects links, nonregular files, noncanonical directory selections,
malformed TOML, invalid UTF-8, changing file identity/size/time, and exceeded
bounds. Each source file is at most 128 KiB; complete selected sources are at
most 384 KiB. Observations contain at most 128 entries and 512 KiB. RPC
selection descriptors are at most 64 KiB. Each inspected candidate has its original
SHA-256 and presence marker, including missing override candidates. A default
instruction file masked by a nonempty override is not read and retains an explicit
shadowed marker. Creating an override after preview changes the proof. No source write, native launch,
provider request, install, login, trust operation or automatic scope discovery
occurs.

Supported settings are model, reasoning effort, sandbox permission, approval
policy and service tier. Native `danger-full-access` maps explicitly to
DeliDev `full-access`; other selectors retain their reviewed values. Preview
classifies unsupported and excluded entries without returning their values or
native key text. Authentication, credentials, managed restrictions and generated
memory are excluded. Instruction text with credential markers is excluded.
Hooks, plugins and MCP produce only closed inert category descriptors: original
commands, environment values, endpoints, identities, package paths and trust
flags are never imported. These descriptors grant no execution, trust or
credential authority.

Apply independently selects exact setting, instruction and inert-package entry
IDs and binds the complete original preview digest. Instructions attach to the
selected Agent only when `attach_instructions` is explicitly true; otherwise
packages remain independently selectable. Server/project import creates
independent instruction packages without changing an Agent. Selected settings
follow the original preview order, so a selected later layer overrides a selected
earlier value of the same field. Unselected entries never become configuration.
Agent model import changes only the selected route, preserving account links,
route order, weights and other inline sources.

The accepted preview and apply jobs retain the authenticated actor, server,
Worker device, original process instance, machine revision and destination
revision. Acceptance, dispatch and result publication revalidate all authorities.
A new apply must use a successful original preview no older than 24 hours.
The original Worker rereads the exact sources and the server compares the entire
canonical observation digest before publication. Source drift, Worker replacement,
revocation, incompatible selections, cancellation or destination conflicts publish
an unchanged-configuration failure. Validation precedes every configuration write;
selected settings, all packages, events, successful job outcome and report receipt
publish in one transaction. Storage failure rolls back the complete report and
retains its original retry identity.

Request receipts contain only the original job reference. Exact replay observes
that job before freshness checks and never reads a replacement source or creates
another package, including after expiry, restart or later package deletion.
Apply outcomes retain only resource references and revisions, without another
instruction/configuration copy. Existing active execution snapshots and source
files remain unchanged. Logs contain only operation/request/job/Worker identity,
closed state and counts; no source paths, keys, values or contents are logged.

Imported Templates carry a closed version-1 `native_import` descriptor and use
resource schema 2. Existing mutable Templates stay schema 1. Imported packages
are immutable at the storage boundary; ordinary saves cannot remove or change
that descriptor. Their source digest, harness, package kind, order and complete
contents survive portable version-7 export, preview, apply and exact reuse.
Malformed/null metadata rejects. Inert packages and incompatible harness packages
cannot be attached to Agents through direct saves, portable imports or execution
admission. Ordinary explicit deletion remains subject to existing reference checks;
deleting a package neither rewrites an execution snapshot nor allows its original
import receipt to recreate it. Importing a fresh package requires a new explicit
request and preview.

Regression fixtures must cover selected layers/profiles, override precedence,
secret exclusion, independent entry selection, all three destinations, source and
destination conflicts, original Worker replacement/revocation, atomic publication,
immutable/inert packages, portable preservation, exact replay and unchanged
active snapshots. Fixtures do not establish installed-native/account acceptance.

## Explicit Worker-local Claude imports

The same version-1 preview/apply RPCs, CLI commands, capabilities, receipts and
transaction boundaries also accept `harness: "claude-code"`. The caller explicitly
selects the source directories and one destination: server defaults, a selected
Project, or a selected Agent Worker route. Destination scopes must identify Claude
Code and retain their current revision. Settings, instructions and extension
packages remain independently selectable. No account, provider, native runtime,
active execution snapshot or host source is changed by preview.

A home scope names the Claude configuration directory itself and reads only
`settings.json` and `CLAUDE.md`. Each selected project directory reads
`.claude/settings.json`, then `.claude/settings.local.json`, followed by
`CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md` and `.mcp.json`. Selected project
scopes form an explicit ancestor-to-descendant chain. Settings retain that source
order; selecting later values overrides selected earlier values of the same field.
Instruction files are additive and retain their exact UTF-8 bytes and order. The
[Claude settings documentation](https://code.claude.com/docs/en/settings) describes
the separate user/project/local settings layers; the
[Claude memory documentation](https://code.claude.com/docs/en/memory) describes
instruction files, rules and native file imports.

A Claude scope may additionally name at most 64 individual relative `files` under
`rules/`, `skills/<name>/` or `plugins/<name>/`; project names include the `.claude/`
prefix. Rule files must end in `.md`. These exact files follow the fixed candidates
in the descriptor's reviewed order. Profile selectors, absolute paths, traversal,
credential/managed/memory filenames and links are rejected. No directory scan,
manifest reference, native `@import`, marketplace, plugin cache or skill reference
can expand the read set. Auxiliary extension files must be individually selected.
The bounded regular-file reader retains presence and original SHA-256 for every
candidate, including absent files. Adding, deleting or changing any selected source
invalidates the complete preview before atomic publication. The existing source,
observation, entry and descriptor bounds apply. Malformed settings/MCP JSON fails;
non-UTF-8 auxiliary files are classified as unsupported without their contents.

Supported ordinary values are `model`, `effortLevel` and
`permissions.defaultMode`. Effort accepts `low`, `medium`, `high`, `xhigh` and `max`.
Native permission modes retain their exact Claude values in `claude_permission`;
they are never translated into Codex sandbox or approval settings. Extra permission
rules, directories and unknown native options remain unsupported. Unsupported and
excluded entries disclose neither native key names nor values. Native login,
`.credentials.json`, hybrid `.claude.json` state, managed restrictions, generated
memory, environment maps, credential keys and credential-bearing URLs or text are
excluded. Import does not establish account readiness or permission availability
in an installed native version.

Selected non-secret hooks, enabled plugin declarations, MCP declarations, skill
files and plugin files become immutable inert packages. Each closed envelope keeps
a category, exact selected UTF-8 contents and their SHA-256, with
`execution_authority`, `credential_authority` and `trust` fixed to false. An envelope
must fit the 128 KiB package bound; larger encoded content is unsupported. The
Worker and portable package validator apply the same credential exclusion. Rules
with frontmatter or native file import directives also retain their bytes as inert
packages, so conditional instructions cannot become unconditional Agent prompts.
Plain selected rules can become instruction packages. No selected extension can
be attached to an Agent or executed through import, direct save or portable
transfer; later activation requires its separate trust and credential contract.

Claude regression fixtures cover ordered user/project/local layers, original
instruction bytes, exact selected rules/skills/plugins, secret exclusion, inert
conditional rules/import directives, link/bound/malformed-file rejection and source
presence drift. Shared transaction fixtures exercise both harnesses at all three
destinations, revocation/cancellation/replacement, destination conflicts, rollback,
immutable portable packages and exact receipt replay after expiry/restart/deletion.
These fixtures are separate from installed native, account and platform acceptance.
