# DeliDev image input contract

## Scope and ownership

Issue #1746 owns first-message and follow-up still-image inputs for project sessions and General Chat. File selection, clipboard images and drag/drop share one ordered draft. Image-only messages are valid. Text retains its independent 256 KiB limit. Each message admits at most eight images, 10 MiB per image and 40 MiB combined. Accept original PNG, JPEG and WebP bytes only after content decoding; reject animation, corruption, mismatched media types and unsafe decode allocations. Do not resize, convert, OCR or replace images with text.

The owner explicitly permits the complete capability/protocol declarations and their activation in one feature PR for this issue. System capability 45, Worker capability 25, and `CreateSessionRequest.attachments` / `EnqueueInputRequest.attachments` field 5 belong exclusively to this feature. The ordinary main-first reservation rule remains mandatory for unrelated features. No SQLite migration is authorized.

## Transfer and immutable input authority

`AttachmentService` owns authenticated BeginUpload, WriteChunk, FinishUpload, GetUpload, ReadAttachment and DeleteDraftAttachment. Worker-only WatchAttachmentTransfers and ReportAttachmentTransfer relay bytes through the owning authenticated Worker stream. A chunk is at most 256 KiB and binds its offset and SHA-256. Identical repeated writes are idempotent; conflicting bytes fail. Finish validates actual content, complete length and full digest before readiness. The server retains bounded metadata only; bytes remain in private DeliDev-owned Worker storage and never enter resource, receipt or assignment JSON.

Staging binds the authenticated actor, original draft/operation UUID-v7 identities, explicit Runner ID/revision and optional original session. Acceptance atomically claims the exact ordered ready references for one original input. Image references contain only opaque ID, Runner ID, media type, length and full SHA-256. Paths and names grant no filesystem authority. Exact uncertain retries retain original input identity and cannot create duplicate claims or native sends. Replacing a Runner invalidates its draft references and requires explicit transfer to the new selection.

## Native delivery and recovery

Adapters deliver actual native image primitives under the selected original account/model/harness, permissions and execution. Enable a route only with positive source/schema and automated fixture evidence; unsupported combinations preserve the draft and report a typed error. Codex source-backed image delivery uses the native image input union, with Worker-owned private paths or data URLs only when required by that original native protocol. Other harnesses require their independent image-input and history proofs. Image-bound Steer remains unavailable without a complete separate proof.

Ordered image identity joins immutable assignment, acknowledgment, transcript and recovery comparisons. Legacy text-only identities remain byte-compatible. Never reconstruct missing native image history, silently omit a part, infer successful native delivery or resend an uncertain input. Image readback is authenticated and scoped to the retained session/reference; no public URLs or desktop-local paths are authority.

## Lifetime and deletion

Same-identity project and General Chat drafts survive ordinary navigation, Settings and reconnect. Identity changes dispose draft presentation and private preview URLs. Explicit removal deletes only owned unclaimed staging, through a durable pending obligation while the Runner is offline. Archive preserves accepted bytes. Independent Fork retains attachment ownership throughout its own lifetime; Sidechat workspace references acquire no parent attachment deletion ownership.

Accepted attachment cleanup joins the existing durable session deletion and backup/restore quarantine boundaries. Removal completion requires observed absence on the original Worker. Preserve images while independently owned, preserve unsettled obligations across restart, and never delete original Local files or shared account resources. No automatic retention eviction is authorized.

## Validation and observability

Log safe operation/session/Runner IDs, phases, counts and typed failures only. Exclude image bytes, filenames, paths, prompts and native payloads. Automated validation covers content/limits, chunk replay/conflicts, actor/Runner/session/reference authority, atomic acceptance, restart/uncertainty, readback, archive/Fork/Sidechat and confirmed cleanup.

The owner assigns real-account, installed-native, remote-machine and platform acceptance to their separate verification work. These checks do not block this feature's source/schema and automated-fixture acceptance. Record unperformed acceptance separately from passing fixtures/builds in PRs and CI; this exception does not weaken runtime ownership, capability negotiation or recovery authority.
