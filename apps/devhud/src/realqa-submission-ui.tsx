import { UploadContentType, UploadQuery } from "@delinoio/devhud-api-client";
import { useMutation } from "@connectrpc/connect-query";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { Copy } from "./localization.ts";
import { createGitHubProvider, GitHubErrorCode, GitHubProviderError, readGitHubCredential, type GitHubProvider, type GitHubRepositoryRef } from "./github-provider.ts";
import type { CaptureDraft, NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary.tsx";
import { composeIssueBody, decodeSha256Hex, editableBrowserDiagnostics, parseEditableBrowserDiagnostics, stripFinalSubmissionMarker } from "./realqa-submission.ts";
import { uploadOfficialImages, uploadR2Images } from "./realqa-upload.ts";

interface SubmissionModalProps {
  readonly draft: CaptureDraft;
  readonly bridge: NativeBridgeV1;
  readonly copy: Copy;
  readonly onClose: () => void;
  readonly onConfirmed: () => Promise<void>;
  readonly provider?: GitHubProvider;
}

export function RealqaSubmissionModal({ draft, bridge, copy, onClose, onConfirmed, provider: injectedProvider }: SubmissionModalProps) {
  const identity = useIdentitySettings();
  const createUpload = useMutation(UploadQuery.createUpload);
  const finalizeUpload = useMutation(UploadQuery.finalizeUpload);
  const deleteUpload = useMutation(UploadQuery.deleteUpload);
  const provider = useMemo(() => injectedProvider ?? createGitHubProvider({ fetch: globalThis.fetch }), [injectedProvider]);
  const repositories = useMemo(() => submissionRepositories(identity.settings), [identity.settings]);
  const initialRepository = repositories.find((entry) => entry.owner.toLowerCase() === identity.settings.github.issueTracker?.owner.toLowerCase() && entry.name.toLowerCase() === identity.settings.github.issueTracker?.repository.toLowerCase()) ?? repositories[0];
  const [repositoryKey, setRepositoryKey] = useState(initialRepository ? repositoryKeyFor(initialRepository) : "");
  const [profileRef, setProfileRef] = useState(initialRepository?.profileRef ?? identity.settings.github.profiles[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [selectedImages, setSelectedImages] = useState<ReadonlySet<string>>(() => new Set(draft.images.map((image) => image.id)));
  const [diagnostics, setDiagnostics] = useState<string | null>(() => draft.browserContext ? editableBrowserDiagnostics(draft.browserContext.context) : null);
  const [labels, setLabels] = useState<readonly string[]>([]);
  const [selectedLabels, setSelectedLabels] = useState<ReadonlySet<string>>(new Set());
  const [uploadProvider, setUploadProvider] = useState<"official" | "r2">(identity.settings.uploads.provider);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [createdUrl, setCreatedUrl] = useState<string | null>(null);
  const [cleanupPending, setCleanupPending] = useState(false);
  const dialog = useRef<HTMLElement>(null);
  const titleInput = useRef<HTMLInputElement>(null);
  const selectedRepository = repositories.find((entry) => repositoryKeyFor(entry) === repositoryKey) ?? null;
  const selectedProfile = identity.settings.github.profiles.find((entry) => entry.id === profileRef) ?? null;
  const r2 = identity.settings.uploads.r2;
  const selectedImageCount = selectedImages.size;

  useEffect(() => { titleInput.current?.focus(); }, []);
  useEffect(() => {
    let cancelled = false;
    setLabels([]);
    setSelectedLabels(new Set());
    if (!selectedRepository || !selectedProfile) return () => { cancelled = true; };
    void (async () => {
      try {
        const tracker = identity.settings.github.issueTracker;
        const defaults = new Set(tracker && tracker.owner.toLowerCase() === selectedRepository.owner.toLowerCase() && tracker.repository.toLowerCase() === selectedRepository.name.toLowerCase() ? tracker.labels : []);
        const credential = await readGitHubCredential(bridge, selectedProfile, await identity.githubPatScopeId);
        const collected: string[] = [];
        for (let page: number | null = 1; page !== null;) {
          const response = await provider.listLabels(credential, selectedRepository, { page });
          collected.push(...response.items.map((label) => label.name));
          page = response.nextPage;
        }
        if (!cancelled) { setLabels(collected); setSelectedLabels(new Set(collected.filter((label) => defaults.has(label)))); }
      } catch { if (!cancelled) setError(copy.issueLabelsFailed); }
    })();
    return () => { cancelled = true; };
  }, [bridge, copy.issueLabelsFailed, identity.githubPatScopeId, identity.settings.github.issueTracker, provider, repositoryKey, profileRef]);

  const close = () => { if (!busy) onClose(); };
  const keyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); return; }
    if (event.key !== "Tab") return;
    const focusable = dialog.current?.querySelectorAll<HTMLElement>("button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href]");
    if (!focusable?.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  const toggleImage = (imageId: string) => setSelectedImages((current) => {
    const next = new Set(current); if (next.has(imageId)) next.delete(imageId); else next.add(imageId); return next;
  });
  const toggleLabel = (label: string) => setSelectedLabels((current) => {
    const next = new Set(current); if (next.has(label)) next.delete(label); else next.add(label); return next;
  });

  const submit = async () => {
    if (busy || createdUrl || !selectedRepository || !selectedProfile || title.trim() === "") return;
    if (!repositories.some((entry) => repositoryKeyFor(entry) === repositoryKey && entry.profileRef === profileRef)) { setError(copy.issueRepositoryCredentialMismatch); return; }
    if (uploadProvider === "r2" && (!r2 || !r2.accountId || !r2.publicBaseUrl)) { setError(copy.issueR2SetupRequired); return; }
    let parsedDiagnostics = null;
    try { parsedDiagnostics = diagnostics === null ? null : parseEditableBrowserDiagnostics(diagnostics); }
    catch { setError(copy.issueDiagnosticsInvalid); return; }
    setBusy(true); setError(null); setStatus(copy.issueSubmitting);
    const finalizedUploadIds: string[] = [];
    let githubAttempted = false;
    try {
      let imageUrls: readonly string[] = [];
      if (selectedImageCount > 0) {
        const flattenedResponse = await bridge.request({ operation: "capture.flatten", draftId: draft.id, expectedRevision: draft.revision });
        if (flattenedResponse.kind !== "capture-flattened") throw new Error("flatten-response");
        const flattened = draft.images.flatMap((image) => selectedImages.has(image.id) ? flattenedResponse.images.filter((item) => item.imageId === image.id) : []);
        if (flattened.length !== selectedImageCount) throw new Error("flatten-count");
        if (uploadProvider === "official") {
          const uploaded = await uploadOfficialImages(flattened, {
            create: async (image, group) => {
            const reserved = await createUpload.mutateAsync({
              target: { target: group === null ? { case: "newSubmission", value: {} } : { case: "existingGroup", value: { submissionId: uuid(group.submissionId), uploadGroupId: uuid(group.uploadGroupId) } } },
              expectedSizeBytes: BigInt(image.bytes), expectedSha256: decodeSha256Hex(image.sha256), contentType: UploadContentType.PNG,
            });
            const reservation = reserved.reservation;
            if (!reservation?.uploadId || !reservation.submissionId || !reservation.uploadGroupId || !reservation.reservationId || !reservation.requiredHeaders) throw new Error("reservation-contract");
            return {
              uploadId: reservation.uploadId.value,
              submissionId: reservation.submissionId.value,
              uploadGroupId: reservation.uploadGroupId.value,
              reservationId: reservation.reservationId.value,
              stagingGeneration: reservation.stagingGeneration.toString(),
              signedPutUrl: reservation.signedPutUrl,
              requiredHeaders: { contentType: reservation.requiredHeaders.contentType, checksumSha256Base64: reservation.requiredHeaders.checksumSha256Base64, contentLength: reservation.requiredHeaders.contentLength.toString() },
            };
            },
            put: async (image, reservation) => {
            const uploaded = await bridge.request({
              operation: "capture.upload-official", draftId: draft.id, expectedRevision: draft.revision, imageId: image.imageId, expectedBytes: image.bytes, expectedSha256: image.sha256,
              upload: reservation,
            });
            if (uploaded.kind !== "capture-uploaded" || uploaded.observedEtag === "") throw new Error("upload-response");
            return uploaded.observedEtag;
            },
            finalize: async (image, reservation, observedEtag) => {
              const finalized = await finalizeUpload.mutateAsync({ uploadId: uuid(reservation.uploadId), submissionId: uuid(reservation.submissionId), uploadGroupId: uuid(reservation.uploadGroupId), reservationId: uuid(reservation.reservationId), stagingGeneration: BigInt(reservation.stagingGeneration), expectedSizeBytes: BigInt(image.bytes), expectedSha256: decodeSha256Hex(image.sha256), observedEtag });
              if (!finalized.upload || !finalized.upload.uploadId) throw new Error("finalization-contract");
              return { uploadId: finalized.upload.uploadId.value, publicUrl: finalized.upload.publicUrl };
            },
            onFinalized: (uploadId) => finalizedUploadIds.push(uploadId),
          });
          imageUrls = uploaded.urls;
        } else {
          const profile = r2!;
          const nativeProfile = { profileRef: profile.profileRef, endpoint: profile.endpoint, accountId: profile.accountId!, bucket: profile.bucket, publicBaseUrl: profile.publicBaseUrl!, prefix: profile.prefix };
          imageUrls = await uploadR2Images(flattened, nativeProfile, async (image) => {
            const uploaded = await bridge.request({ operation: "capture.upload-r2", draftId: draft.id, expectedRevision: draft.revision, imageId: image.imageId, expectedBytes: image.bytes, expectedSha256: image.sha256, profile: { profileRef: profile.profileRef, endpoint: profile.endpoint, accountId: profile.accountId!, bucket: profile.bucket, publicBaseUrl: profile.publicBaseUrl!, prefix: profile.prefix } });
            if (uploaded.kind !== "capture-uploaded" || uploaded.publicUrl === null) throw new Error("r2-upload-response");
            return uploaded.publicUrl;
          });
        }
      }
      const issueBody = composeIssueBody({ userBody: body, diagnostics: parsedDiagnostics, imageUrls, submissionId: draft.id, diagnosticsSummary: copy.issueBrowserDiagnostics });
      const credential = await readGitHubCredential(bridge, selectedProfile, await identity.githubPatScopeId);
      githubAttempted = true;
      const result = await provider.createIssue(credential, selectedRepository, { title: title.trim(), body: stripFinalSubmissionMarker(issueBody, draft.id), labels: [...selectedLabels], submissionId: draft.id });
      setCreatedUrl(result.issue.url);
      setStatus(copy.issueCreated);
      try { await onConfirmed(); } catch { setCleanupPending(true); setError(copy.issueDraftCleanupFailed); }
    } catch (reason) {
      const ambiguous = reason instanceof GitHubProviderError && reason.code === GitHubErrorCode.AmbiguousWrite;
      if (!githubAttempted || !ambiguous) await Promise.allSettled(finalizedUploadIds.map((uploadId) => deleteUpload.mutateAsync({ uploadId: uuid(uploadId) })));
      setError(ambiguous ? copy.issueAmbiguous : copy.issueSubmissionFailed);
      setStatus("");
    } finally { setBusy(false); }
  };

  const retryCleanup = async () => {
    setBusy(true); setError(null);
    try { await onConfirmed(); setCleanupPending(false); onClose(); }
    catch { setError(copy.issueDraftCleanupFailed); }
    finally { setBusy(false); }
  };

  return <div className="overlay" role="presentation"><section ref={dialog} className="issue-dialog" role="dialog" aria-modal="true" aria-labelledby="issue-dialog-title" onKeyDown={keyDown}>
    <h3 id="issue-dialog-title">{copy.issueModalTitle}</h3>
    <label>{copy.issueRepository}<select value={repositoryKey} disabled={busy || !!createdUrl} onChange={(event) => { const repository = repositories.find((entry) => repositoryKeyFor(entry) === event.target.value); setRepositoryKey(event.target.value); if (repository) setProfileRef(repository.profileRef); }}><option value="">{copy.issueSelectRepository}</option>{repositories.map((repository) => <option key={repositoryKeyFor(repository)} value={repositoryKeyFor(repository)}>{repository.owner}/{repository.name}</option>)}</select></label>
    <label>{copy.issueCredential}<select value={profileRef} disabled={busy || !!createdUrl} onChange={(event) => setProfileRef(event.target.value)}><option value="">{copy.githubSelectProfile}</option>{identity.settings.github.profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>
    <label>{copy.issueTitle}<input ref={titleInput} autoFocus value={title} disabled={busy || !!createdUrl} onChange={(event) => setTitle(event.target.value)} /></label>
    <label>{copy.issueBody}<textarea value={body} disabled={busy || !!createdUrl} onChange={(event) => setBody(event.target.value)} /></label>
    <fieldset><legend>{copy.issueImages}</legend>{draft.images.map((image, index) => <label className="check" key={image.id}><input type="checkbox" checked={selectedImages.has(image.id)} disabled={busy || !!createdUrl} onChange={() => toggleImage(image.id)} /><img src={image.previewUrl} alt="" />{copy.editorImage} {index + 1}</label>)}</fieldset>
    {diagnostics !== null && <div className="issue-diagnostics"><label>{copy.issueBrowserDiagnostics}<textarea value={diagnostics} disabled={busy || !!createdUrl} onChange={(event) => setDiagnostics(event.target.value)} /></label><button type="button" disabled={busy || !!createdUrl} onClick={() => setDiagnostics(null)}>{copy.issueRemoveDiagnostics}</button></div>}
    <fieldset><legend>{copy.issueLabels}</legend>{labels.length === 0 ? <p>{copy.issueNoLabels}</p> : labels.map((label) => <label className="check" key={label}><input type="checkbox" checked={selectedLabels.has(label)} disabled={busy || !!createdUrl} onChange={() => toggleLabel(label)} />{label}</label>)}</fieldset>
    <label>{copy.issueUploadProvider}<select value={uploadProvider} disabled={busy || !!createdUrl} onChange={(event) => setUploadProvider(event.target.value as "official" | "r2")}><option value="official">{copy.issueUploadOfficial}</option><option value="r2">{copy.issueUploadR2}</option></select></label>
    <dl><dt>{copy.issueSubmissionPath}</dt><dd>{copy.issueSubmissionPathDirect}</dd></dl>
    {selectedImageCount > 0 && <p className="notice">{copy.issuePublicImageWarning}</p>}
    {status && <p role="status" aria-live="polite">{status}</p>}{error && <p role="alert" className="native-setting-error">{error}</p>}
    {createdUrl && <p><a href={createdUrl} target="_blank" rel="noreferrer">{createdUrl}</a></p>}
    <div className="actions">{cleanupPending ? <button className="primary" disabled={busy} onClick={() => void retryCleanup()}>{copy.issueRetryDraftCleanup}</button> : <button className="primary" disabled={busy || !!createdUrl || !selectedRepository || !selectedProfile || title.trim() === ""} onClick={() => void submit()}>{copy.issueSubmit}</button>}<button disabled={busy} onClick={close}>{copy.close}</button></div>
  </section></div>;
}

type SubmissionRepository = GitHubRepositoryRef & { readonly profileRef: string };
function submissionRepositories(settings: ReturnType<typeof useIdentitySettings>["settings"]): readonly SubmissionRepository[] {
  const entries: SubmissionRepository[] = settings.github.repositories.flatMap((entry) => entry.profileRef ? [{ owner: entry.owner, name: entry.name, profileRef: entry.profileRef }] : []);
  const tracker = settings.github.issueTracker;
  if (tracker?.profileRef) entries.unshift({ owner: tracker.owner, name: tracker.repository, profileRef: tracker.profileRef });
  return [...new Map(entries.map((entry) => [repositoryKeyFor(entry), entry])).values()];
}
function repositoryKeyFor(repository: GitHubRepositoryRef): string { return `${repository.owner.toLowerCase()}/${repository.name.toLowerCase()}`; }
function uuid(value: string): { readonly value: string } { return { value }; }
