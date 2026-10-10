# DeliDev image input contract

## Scope and ownership

The feature owns first-message and follow-up still-image inputs for project sessions and General Chat. File selection, clipboard images and drag/drop share one ordered draft. Image-only messages are valid. Text retains its independent 256 KiB limit. Each message admits at most eight images, 10 MiB per image and 40 MiB combined. Accept original PNG, JPEG and WebP bytes only after content decoding; reject animation, corruption, mismatched media types and unsafe decode allocations. Do not resize, convert, OCR or replace images with text.

The owner explicitly permits the complete capability/protocol declarations and their activation in one feature PR for this issue. System capability 45, Worker capability 25, and `CreateSessionRequest.attachments` / `EnqueueInputRequest.attachments` field 5 belong exclusively to this feature. All feature allocations follow the [shared allocation workflow](cmds-delidev-structure-contract.md#allocation-workflow); a separate reservation PR or prior merge to main is not required. No SQLite migration is authorized.

## Transfer and immutable input authority

`AttachmentService` owns authenticated BeginUpload, WriteChunk, FinishUpload, GetUpload, ReadAttachment and DeleteDraftAttachment. Worker-only WatchAttachmentTransfers and ReportAttachmentTransfer relay bytes through the owning authenticated Worker stream. A chunk is at most 256 KiB and binds its offset and SHA-256. Identical repeated writes are idempotent; conflicting bytes fail. Finish validates actual content, complete length and full digest before readiness. The server retains bounded metadata only; bytes remain in private DeliDev-owned Worker storage and never enter resource, receipt or assignment JSON.

Staging binds the authenticated actor, original draft/operation UUID-v7 identities, explicit Runner ID/revision and optional original session. Acceptance atomically claims the exact ordered ready references for one original input. Image references contain only opaque ID, Runner ID, media type, length and full SHA-256. Paths and names grant no filesystem authority. Exact uncertain retries retain original input identity and cannot create duplicate claims or native sends. Replacing a Runner invalidates its draft references and requires explicit transfer to the new selection.

## Native delivery and recovery

Adapters deliver actual native image primitives under the selected original account/model/harness, permissions and execution. Enable a route only with positive source/schema and automated fixture evidence; unsupported combinations preserve the draft and report a typed error. Codex source-backed image delivery uses the native image input union, with Worker-owned private paths or data URLs only when required by that original native protocol. Other harnesses require their independent image-input and history proofs. Image-bound Steer remains unavailable without a complete separate proof.

Ordered image identity joins immutable assignment, acknowledgment, transcript and recovery comparisons. Continuation, compaction, original lost-report recovery and first Fork-child dispatch retain the complete ordered input. Original queue-to-assignment authority and startup retries compare text, mode, skill bindings and attachment references; a matching prompt alone cannot authorize native publication or recovery. Legacy text-only identities remain byte-compatible. Empty image selections do not require an active Runner or Worker registration at queue acceptance; ordinary session, archive, queue and receipt rules remain authoritative. Nonempty image claims retain the original Runner revision and Worker Device checks. Never reconstruct missing native image history, silently omit a part, infer successful native delivery or resend an uncertain input. Image readback is authenticated and scoped to the retained session/reference; no public URLs or desktop-local paths are authority.

## Lifetime and deletion

Same-identity project and General Chat drafts survive ordinary navigation, Settings and reconnect. Identity changes dispose draft presentation and private preview URLs. Explicit removal deletes only owned unclaimed staging, through a durable pending obligation while the Runner is offline. Archive preserves accepted bytes. Independent Fork retains attachment ownership throughout its own lifetime; Sidechat workspace references acquire no parent attachment deletion ownership.

Each session retains at most 4,096 distinct unresolved image references across accepted or removed inputs, current independent Fork ownership and session-tied unclaimed drafts. Upload admission, input claims and Fork owner publication enforce this capacity in the same transaction before new metadata or Worker bytes. Replays and existing-reference transitions do not consume another slot. Deleting or quarantined obligations retain their slots; only confirmed terminal byte removal or release to another independent owner frees the original scope. Cleanup and Fork inventory use this same current-obligation set, excluding terminal draft history without erasing its receipt or proof. Keep the per-input eight-image, per-file ten-MiB and per-input forty-MiB bounds independently.

Accepted attachment cleanup joins the existing durable session deletion and backup/restore quarantine boundaries. Removal completion requires observed absence on the original Worker. Preserve images while independently owned, preserve unsettled obligations across restart, and never delete original Local files or shared account resources. No automatic retention eviction is authorized.

## Validation and observability

Log safe operation/session/Runner IDs, phases, counts and typed failures only. Exclude image bytes, filenames, paths, prompts and native payloads. Automated validation covers content/limits, chunk replay/conflicts, actor/Runner/session/reference authority, atomic acceptance, restart/uncertainty, readback, archive/Fork/Sidechat and confirmed cleanup.

The owner assigns real-account, installed-native, remote-machine and platform acceptance to their separate verification work. These checks do not block this feature's source/schema and automated-fixture acceptance. Record unperformed acceptance separately from passing fixtures/builds in PRs and CI; this exception does not weaken runtime ownership, capability negotiation or recovery authority.

## Closed adapter profiles

Codex is the enabled image route. The official V2 `UserInput` union at `0.151.0` revision `d8673cb68e349c208659b986697773d3145dbb14` and `0.159.2` revision `8b9fa496bbf2c47aebd62e85a080b9a522a455b5` defines `localImage` with a native path and preserves that primitive in user-message history. See the pinned [0.151.0 turn schema](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/app-server-protocol/src/protocol/v2/turn.rs), [0.159.2 turn schema](https://github.com/openai/codex/blob/8b9fa496bbf2c47aebd62e85a080b9a522a455b5/codex-rs/app-server-protocol/src/protocol/v2/turn.rs), and [original user-message item](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/protocol/src/items.rs). DeliDev resolves only validated private Worker bytes into this primitive. The pinned [image loader](https://github.com/openai/codex/blob/d8673cb68e349c208659b986697773d3145dbb14/codex-rs/utils/image/src/lib.rs) detects format from bytes, so the opaque private filename supplies no media authority. Before acceptance, every configured first-input source model must positively declare image input in saved metadata. The selected declaration is frozen as the optional `image_input_declared` configuration value; absent text-only profiles retain their original omission and digest. Follow-ups use this original snapshot, and receipt retries do not rerun current catalog selection. This declaration is an eligibility check and grants no native input authority. Before sending, the exact immutable native model must advertise the `image` input modality through the bounded native model snapshot. Text-only models, missing bytes and unresolved native metadata fail before input delivery. Native user-message and continuation comparisons resolve the original journal and compare the complete ordered input digest.

Claude Code, OpenCode and Grok Build return the explicit unsupported-image error before session acceptance or native work. This states the DeliDev adapter boundary; it does not assert that every upstream interface lacks images. The pinned Claude SDK [message union](https://github.com/anthropics/claude-agent-sdk-python/blob/36f95486ee9fc49d8ee1ed56811f07b5e8e23ac6/src/claude_agent_sdk/types.py) does not supply a closed image block in its retained message type, while DeliDev's original stream controller and recovery history bind one textual input. OpenCode's pinned [prompt schema](https://github.com/anomalyco/opencode/blob/545f51d26cc39a907d2867492d498d9607ea5fa4/packages/schema/src/prompt.ts) supplies file attachments, but DeliDev's accepted input and checkpoint profile still proves one textual part and has no complete image-history ownership adapter. These routes require their own exact input, acknowledgment, model, history and cleanup proofs before activation.

Grok Build's official [ACP image implementation](https://github.com/xai-org/grok-build/blob/2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8/crates/codegen/xai-grok-shell/src/session/acp_session_impl/prompt_build.rs) can describe images and render the description into a textual user message. That route cannot satisfy this contract's original image delivery requirement. This source revision also does not establish an exact `1.0.46` image/history profile. DeliDev retains its closed original text-only ACP input and makes no image claim for Grok Build.

Automated route fixtures stage real owned images and assert that all three unsupported harnesses leave the original upload ready and unclaimed. Unsupported guidance preserves the draft. Positive Codex fixtures use the schema-defined primitive, image-only and text-with-images requests, exact selected model modality, ordered receipt/history metadata and changed-byte rejection. These controlled fixtures do not execute an installed native harness or establish real-account/platform acceptance.

Queued edits include typed attachment field 5. Explicit selections must match the immutable original ordered references; old-client omission preserves them. Mixed skill/image Fork proofs retain the original Worker image lookup while copying child-owned skill packages; no new byte ownership or native call is inferred.

## Image-view observation separation
[Codex image-view metadata](cmds-delidev-harness-contract.md#codex-image-view-observations) uses the original tool/reference identity, not an image-input attachment. Its native location and prepared-root scope grant no source file read, transfer, image-input claim or source-file deletion. Existing authenticated attachment reads continue to require an original claimed upload and immutable byte ownership; an image-view observation alone cannot satisfy them. The metadata-only transcript explicitly reports unavailable preview. Do not reopen a displayed path or download a URL to supply missing native bytes. Existing attachment cleanup and independent workspace/native cleanup retain their original authority.
## Pre-send image rejection classification
Follow the direct startup contract's positive image rejection profile. The exact original native model modality response may reject image preparation before turn/start; model names or generic error codes do not establish support or send status. Retain the opaque original adapter proof, exact synchronized Worker claim and independent cleanup before publishing IMAGE_INPUT_REJECTED. Missing or changed proof remains uncertain. Preserve the accepted original prompt and ordered image references, authenticated image ownership and existing explicit retry guards; do not replace attachments, reconstruct native history or resend from observation. The optional startup field 11 and enum values 0/1 add no capability or migration.

## Original native generated images

The feature records System capability 61 and Worker capability 35 with its complete implementation. These are independent of input-image capabilities 45/25. No database migration or generation-service bridge is introduced. The supported native route is the original managed Codex subscription configuration whose effective built-in OpenAI provider and protected authentication have been independently validated. Custom API proxy routes and read-only Sidechat do not gain image generation, provider substitution or inferred entitlement from these allocations. New assignments freeze image-generation admission only when that original managed root route and Worker capability 35 are selected. Historical assignments retain their original disabled profile. Request and verify the actual native `features.image_generation` setting before input; it grants neither account entitlement nor custom-provider support. The native provider still decides actual account availability and reports its original failure.

The pinned Codex 0.162.0 source (`c1382380de69521303b416720a52f42d51af6248`) exposes `imageGeneration` items with original call ID, status, nullable revised prompt, base64 result, optional transparency, optional saved path and nullable usage-limit failure. Each completed native call exposes one PNG; multiple calls preserve publication order. Internal backend generation/request IDs are deliberately absent from this wire. Image-specific token/cost usage is also absent. Preserve actual existing turn-usage observations separately; never synthesize backend IDs or usage. Reject unknown shapes, malformed success bytes and foreign or terminal turn observations. Native saved paths are discarded and never become read or deletion authority.

Only this validated managed route admits 16 MiB native frames and a 32 MiB bounded event queue for the original base64 observations. Decode original still PNG bytes with the existing 10 MiB and 40-million-pixel limits. Managed read-only Sidechat may read the same bounded original image-bearing parent history while its generation feature remains explicitly disabled. Other native routes retain their existing frame/queue limits. History containers must admit the same closed bounded original image-bearing turn without reconstructing or truncating its original identity.

The original Worker durably records execution/job/instance/machine ownership and ordered call-to-reference intent before writing bytes or publishing metadata. A replay resolves the same immutable digest and reference. The server stores only ordered artifact observations and opaque image metadata, under the original execution lease and publishing Worker/device. Authenticated attachment readback uses the existing original Worker transfer path, with exact chunk and full-image checksums. Successes before a later native failure remain visible without claiming the whole turn succeeded.

Deletion freezes independent owners, preserved generated references and every originally admitted execution generation, even if no output metadata was published. Frozen work requires Worker capability 35 before a first acknowledgment; a downgraded Worker cannot skip unpublished controller intents. Original acknowledged receipts remain replayable without replacing their revision. No cleanup acquires newer output references. After the original native execution and workspace owners have joined, the original Worker enumerates its durable controller, including an image whose publication acknowledgment was lost. A last-owner deletion removes only the original owned stored bytes; an independent Fork's retained reference protects them until that owner is explicitly deleted. Read-only Sidechat borrows references without owning bytes. Tombstones fence delayed recreation. Native saved files and explicit exported user copies are outside this cleanup authority.

Native/account/platform observations remain separate from automated decoder, authenticated transfer, partial-publication, durable-controller and independent-owner fixtures. Allocations and fixtures grant no account entitlement or proof of installed native behavior.

## Project requirements

- The feature image inputs follow `cmds-delidev-image-input-contract.md`. The owner permits declarations and complete activation in the same feature PR for System 45, Worker 25 and creation/enqueue attachments field 5 only. Preserve metadata-only server storage, authenticated Worker byte ownership, ordered immutable claims, original native image proofs and durable independent cleanup; no migration. Real-account, installed-native, remote-machine and platform acceptance remain owner-assigned and nonblocking for this feature.

## cmds/delidev-cli/internal/domain constraints

- Windows service process ownership resolves the reported Win32 image path under the installation's canonical path contract, including 8.3 aliases, and matches the original executable file identity. Preserve independent SID and before/after process-birth checks.

- The feature optional startup failure kind is closed and zero-omitted. IMAGE_INPUT_REJECTED requires Failed/Input/Codex/Unsupported/NotSent/confirmed cleanup; generic errors and mixed metadata cannot acquire image provenance. Follow the image-input and direct startup contracts.

## cmds/delidev-cli/internal/harness/codex constraints

- Image inputs use the pinned V2 localImage primitive under `cmds-delidev-image-input-contract.md`. Require the exact immutable model to advertise image modality before send, resolve only original validated private Worker bytes, and bind ordered refs into acknowledgment/history/recovery digests. Never publish native paths, convert images to text, omit image parts or enable image-bound Steer without a separate complete profile.

- The feature image rejection proof is private to original image preparation before turn/start and binds request/input/full ordered input digest. Generic errors, attachment resolution, transport/acknowledgment and successful native sends cannot grant the proof. Preserve exact native model modality and localImage/history verification under the image-input and direct startup contracts.

## cmds/delidev-cli/internal/imageinput constraints

- Store original image bytes only in private Worker-owned storage. Caller paths, filenames and native event paths grant no authority. Resolve native paths through the original closed reference, paired Runner and validated bounded content.

## cmds/delidev-cli/internal/providers constraints

- OpenRouter inspection requests `output_modalities=text` for every selected API format. Retain image/audio input models that produce text, exclude non-text-only generation models, and preserve strict positive context-limit validation. A filtered catalog never deletes previously saved models or grants inference authority.

## cmds/delidev-cli/internal/server constraints

- Image inputs follow `cmds-delidev-image-input-contract.md`: bind original actor/draft/operation, admitted Runner revision and paired Worker device; claim ordered ready references atomically with original input acceptance. Forward bounded authenticated chunks without durable bytes, preserve immutable queue edits and original execution snapshots, and join only explicit unclaimed-draft removal retries. Accepted images belong to session deletion after native cleanup; quarantine blocks restored input/readback.

- Hold the shared credential gate through managed restore eligibility and publication. Private network publication/deletion intents block replacement, and the current safety image retains network profiles/generations/selections; historical routing cannot replace current explicit authority.

- Empty image selections preserve ordinary text-only queue admission and receipt replay without image-specific Runner or Worker registration checks. Nonempty image claims retain their original actor, operation, Runner revision and Worker Device fences.

- Image Begin, claim and metadata updates use the store-owned session capacity fence before writing new metadata or Worker bytes. Preserve existing per-input limits and exact actor/operation/Runner authority; capacity rejection never admits or retires an uncertain image. Follow the image-input contract.

- The feature accepts image startup provenance only for the closed settled Input-phase Codex rejection with original ready process identity, native thread without acknowledged turn and exact claimed queue/input/request/images. Preserve original receipts, prompt/ordered image references, pending capacity settlement and explicit original-selection retry; malformed or mixed reports fail closed. Follow the image-input and direct startup contracts.

## cmds/delidev-cli/internal/store constraints

- Current-only inline-model storage is schema 32. Earlier live databases and backup images fail before writable admission without changing original bytes or sidecars. Historical allocation records and frozen schemas retain provenance; their former upgrade rules grant no current migration path. Preserve independent native/credential, deletion and retained restore-image cleanup receipts. New Agents retain exact source/native-ID inline routes; Model resources and historical estimate backfills are retired.

- Backup publication records the original image metadata and digest in live SQLite before a no-replace rename. Recovery must match that independent claim; same-server identity or a known filename alone never establishes creation provenance. Preserve unclaimed/replaced images as recovery evidence.

- Backup deletion atomically claims images without replacement into private `backup-removals/`, synchronizes both directories, revalidates the full claimed image and sidecars, and only unlinks the claim. Resume retained claims after restart; preserve changed claims, reopened original paths and sidecars as pending recovery evidence. Never unlink the externally known image name after a pathname-only validation.

- Completed backup-deletion maintenance validates the retained intent and image absence without repeated fsync. Preserve synchronization for uncertain recovery and each actual unlink, and continue removing matching reappearing images.

- Backup creation recovery must independently match the published image's server identity to the live scope. Validate immutable SQLite images without adjacent WAL/SHM/journal state, preserving foreign or corrupt originals instead of adopting them by filename. Migration backup validation follows the same sidecar refusal.

- Permanent session deletion must bind its final backup inventory to the exact images inspected without session content under the backup publication gate. Preserve new/replaced images as pending until a fresh classification pass.

- Managed restore rejects unfinished external session deletion, redacts shared remediation/session activity before tombstoned entity removal, and retires exact receipt-owned temporary images before serving a settled outcome. Preserve metadata journals and unaccepted/changed staging; remaining database copies block permanent deletion completion. Pin every staging-migration image in the prepared external journal before publication and verify its exact original fingerprint before cleanup; a legacy journal cannot adopt existing copies. Follow the storage contract.

- Managed restore retains the complete current Device documents from its synchronized external safety image, including browser pending/removed state and original profile/deletion identities. Discard historical Device inventory before copying current records; offline/revoked obligations and completed cleanup cannot be rolled back by an older backup.

- Managed restore refuses any pending private network publication/deletion intent before closing SQLite. Preserve current network profiles, immutable credential generations and server/Worker selections from the safety image, preserving their bodies while freshening resource revisions under the ordinary restore rule; a historical snapshot cannot reactivate outbound authority. The owning server serializes restore with network/account credential operations through the shared gate.

- Copy current machine metadata required by retained Worker network routes from the safety image, replacing historical metadata for those IDs. Owners must be able to read/clear a route and delete its deselected profile without pairing that Worker again. Metadata cannot restore Worker verifiers, instances, grants or native ownership; ordinary restore revocation remains mandatory.

- Installation entities are server-owned operation history. Restore copies current SSH/update entities rather than historical images, and blocks unsettled installation or protected cleanup. Pending projections exclude terminal history without truncating active obligations.

- Image jobs retain empty entity session scope and closed bounded metadata. Independent Fork publication inherits only its accepted boundary prefix; Sidechat borrows original parent references without deletion ownership. Last-owner removal joins synchronized deletion and original Worker acknowledgement. Restore preserves current ownership/removal as quarantined and discards historical authority. Follow the image-input, Fork, Sidechat and storage contracts; add no migration.

- Image admission and Fork ownership atomically retain at most 4,096 distinct unresolved references per session, using the same current-owner and unclaimed/deleting scope as cleanup. Confirmed deleted drafts and released source ownership do not consume capacity; removed inputs, quarantine and uncertain deletion remain retained. Preserve original receipts and proofs; no eviction or migration. Follow the image-input contract.

## cmds/delidev-cli/internal/worker constraints

- Image transfers keep original PNG/JPEG/WebP bytes in private Worker storage under `cmds-delidev-image-input-contract.md`. Validate actual content, size, full digest and bounded decode before readiness or native paths. Immutable deletion tombstones reject delayed writes; completion replay observes exact original absence without removing replacements. Join the authenticated transfer watch with the original primary Worker stream.

- Windows native launch classification must include documented incompatible-image, machine-type and missing-subsystem loader statuses without exposing OS diagnostic strings.

- Last-owner image removal uses original Worker identity after joined native/process cleanup. Image-only plans grant no workspace authority. Persist intent before DELETE; completion and replay require original deletion receipt and absent data/metadata. Reappearing files remain protected. Follow the image-input and storage contracts.

- The feature classifies original image rejection only from the opaque pre-wire adapter proof, exclusive synchronized original assignment/input/request claim and independent confirmed cleanup. Retain claimed uncertainty on missing/changed proof or cleanup, unchanged public receipts and original assignment revisions; never resend by observation. Follow the image-input and direct startup contracts.
