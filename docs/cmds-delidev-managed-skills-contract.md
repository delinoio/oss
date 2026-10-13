# DeliDev Worker-owned managed skills

## Scope

`cmds/delidev-cli/internal/skills` owns the selected Worker's independent managed skill package copies, complete manifests and private original request journals. The server retains bounded safe catalog metadata and Agent selection references. Desktop Skills Settings uses the typed management operations of `SkillService`, which stay distinct from the composer `ListSkills` inventory. Host skill directories, Vercel skills integration, marketplaces, URL installation, automatic updates, package authoring and host configuration adoption are separate or out of scope.

## Runtime and Language

Go owns package copying, digests, persistence and dispatch. TypeScript/React owns disposable Settings presentation through generated Connect Query clients.

## Users and Operators

Users explicitly choose a Runner Device, import an absolute package directory from that Worker's filesystem, inspect it read-only, enable or disable it, delete it after confirmation and select it on Agent Workers. Operators inspect safe request identities, states and bounded metadata without package paths.

## Interfaces and Contracts

System `MANAGED_SKILLS_V1=98` and Worker `MANAGED_SKILLS_V1=60` establish management authority only. `SkillService` adds list, view, import, enable/disable, confirmed delete and explicit original-import cancellation methods. `SkillSelection.managed_revision` and `SKILL_PROVENANCE_MANAGED` identify Worker-owned packages in the existing composer. Typed declarations and all member allocations remain in `protos/delidev/allocations.json`.

Management uses the authenticated owner or paired client actor, the selected machine, authenticated Worker Device and current Worker instance, and exact request and revision checks on the joined workspace reader. It cannot claim an execution job. Metadata never grants path, account or execution authority.

Limits are shared with explicit package snapshots: at most 1,024 regular resources, a 1 MiB `SKILL.md` and 16 MiB of package bytes. Every resource digest covers its path, byte size, content digest and complete permission mode. Import rejects escaping links, nonregular resources, incomplete or changed copies, invalid `SKILL.md` and ambiguous native names without partial publication.

Agent `managed_skills` is an optional object of at most sixteen exact original Worker-device, package and full-content-revision selections in the Agent document. Omission preserves the saved selection on every save surface; a present empty list clears it. The server validates each reference in the saving transaction. Deletion reservations make new references fail before Worker byte removal, and current references block deletion. Portable exports carry opaque IDs and content hashes only; an import marks such an Agent as requiring reconfiguration and grants no destination Worker or package authority. The marker is server-owned: preview adds it to the normalized plan document and the plan and apply validation accept it there, while an incoming bundle that already carries `reconfiguration_required` is rejected.

Enabled selected packages are captured and copied for a future execution generation. Disablement changes only future admission. Accepted complete copies, active executions, continuation and history are immutable and never consult the mutable catalog again. A concurrent Agent revision or enablement change rejects an unaccepted preparation and keeps its original joined cleanup.

## Native discovery

The management foundation supports the accepted Codex native discovery channel. Before input or inference, the original private runtime verifies complete managed copies, sets exact native extra roots and reloads the native skill inventory. Every requested package must appear enabled exactly once at its original path and name; ambiguous names, malformed responses or changed resource modes fail before send. Automatic packages add no structured input part, so a manually typed `$` token remains literal input and explicit `$` selection keeps its existing order and digest semantics. Claude Code, OpenCode and Grok automatic discovery stay explicitly unsupported until their accepted adapters implement the same independent generation and cleanup boundaries. Published per-harness metadata reflects an independently observed verified Runner profile, and every future process verifies again.

## Storage

The existing typed metadata table stores bounded package metadata and actor-bound operations under `managed-skill-v1:` and `managed-skill-operation-v1:`. It stores a digest of the import source selection, never that path or any package bytes. No SQLite migration is added. Accepted managed snapshots participate in the existing 4,096-reference snapshot family bound and the original joined session deletion plan.

The authenticated Worker keeps private package, catalog and request journals. An import claims its intended complete manifest before copied bytes, pins the native root identity, rechecks source identity, content and modes, and publishes last. Private copies keep executable intent with canonical 0700/0600 modes. Legacy explicit-only digests retain their original format.

Completed retries return the original receipt without relaunching work. Interrupted imports never repeat source reads or adopt partial output. Explicit original cancellation fences delayed copying and joins bounded original-root cleanup. A complete import returns its original result and then needs ordinary confirmed catalog deletion. Unknown or replaced roots remain recovery-required. Accepted snapshots and runtime, Fork and Sidechat copies keep independent private ownership and cleanup.

## Desktop

Skills follows MCP in the AI Settings group. A selected Runner scopes search, import, view, enablement, deletion and recovery. Rows show bounded name, description, revision, observed native profiles and current Agent Worker names; disabled rows use neutral wording. View presents literal `SKILL.md` and the resource manifest as inert text. Loading, empty, offline or permission, unsupported, stale or revision-conflict and recovery-required states stay distinct. Unconfirmed mutations retain their exact original request for explicit retry, and an unconfirmed import offers explicit original cleanup. English and Korean presentation, theme tokens, keyboard, Escape and focus return follow the existing Settings dialogs. These fixtures do not establish native, account or packaged platform acceptance.
