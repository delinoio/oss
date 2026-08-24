import { UploadContentType, UploadQuery } from "@delinoio/devhud-api-client";
import { useMutation } from "@connectrpc/connect-query";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { uuidV7 } from "./diagnostics.ts";
import type { Copy } from "./localization.ts";
import { createGitHubProvider, GitHubErrorCode, GitHubProviderError, issueMarker, readGitHubCredential, type GitHubProvider, type GitHubRepositoryRef } from "./github-provider.ts";
import { LocalAgentMode, NativeBridgeError, NativeBridgeErrorCode, type CaptureDraft, type NativeBridgeV1 } from "./native-bridge.ts";
import { localAgentExecutablePath, localAgentHasConsent } from "./local-agent-settings-ui.tsx";
import { useIdentitySettings } from "./service-boundary.tsx";
import { composeIssueBody, decodeSha256Hex, editableBrowserDiagnostics, IssueBodyTooLargeError, IssueTitleInvalidError, parseEditableBrowserDiagnostics, sanitizeIssueTitle, stripFinalSubmissionMarker } from "./realqa-submission.ts";
import { projectedOfficialImageUrls, projectedR2ImageUrls, uploadOfficialImages, uploadR2Images } from "./realqa-upload.ts";

interface SubmissionModalProps {
  readonly draft: CaptureDraft;
  readonly bridge: NativeBridgeV1;
  readonly copy: Copy;
  readonly onClose: () => void;
  readonly onConfirmed: (expectedRevision: number) => Promise<void>;
  readonly provider?: GitHubProvider;
}

export function RealqaSubmissionModal({ draft, bridge, copy, onClose, onConfirmed, provider: injectedProvider }: SubmissionModalProps) {
  const identity = useIdentitySettings();
  const createUpload = useMutation(UploadQuery.createUpload);
  const finalizeUpload = useMutation(UploadQuery.finalizeUpload);
  const deleteUpload = useMutation(UploadQuery.deleteUpload);
  const provider = useMemo(() => injectedProvider ?? createGitHubProvider({ fetch: globalThis.fetch }), [injectedProvider]);
  const repositoryAssociations = useMemo(() => submissionRepositories(identity.settings), [identity.settings]);
  const repositories = useMemo(() => uniqueSubmissionRepositories(repositoryAssociations), [repositoryAssociations]);
  const initialRepository = repositories.find((entry) => entry.owner.toLowerCase() === identity.settings.github.issueTracker?.owner.toLowerCase() && entry.name.toLowerCase() === identity.settings.github.issueTracker?.repository.toLowerCase()) ?? repositories[0];
  const [repositoryKey, setRepositoryKey] = useState(initialRepository ? repositoryKeyFor(initialRepository) : "");
  const initialAssociation = repositoryAssociations.find((entry) => initialRepository !== undefined && repositoryKeyFor(entry) === repositoryKeyFor(initialRepository));
  const [profileRef, setProfileRef] = useState(initialAssociation?.profileRef ?? identity.settings.github.profiles[0]?.id ?? "");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [selectedImages, setSelectedImages] = useState<ReadonlySet<string>>(() => new Set(draft.images.map((image) => image.id)));
  const [diagnostics, setDiagnostics] = useState<string | null>(() => draft.browserContext ? editableBrowserDiagnostics(draft.browserContext.context) : null);
  const [labels, setLabels] = useState<readonly string[]>([]);
  const [selectedLabels, setSelectedLabels] = useState<ReadonlySet<string>>(new Set());
  const [uploadProvider, setUploadProvider] = useState<"official" | "r2">(() => identity.status === "authenticated" ? identity.settings.uploads.provider : "r2");
  const [agentId, setAgentId] = useState("");
  const [agentRunId, setAgentRunId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [createdUrl, setCreatedUrl] = useState<string | null>(null);
  const [cleanupPending, setCleanupPending] = useState(false);
  const [pendingUploadCleanupIds, setPendingUploadCleanupIds] = useState<readonly string[]>([]);
  const dialog = useRef<HTMLElement>(null);
  const titleInput = useRef<HTMLInputElement>(null);
  const submittedRevision = useRef<number | null>(null);
  const selectedRepository = repositories.find((entry) => repositoryKeyFor(entry) === repositoryKey) ?? null;
  const selectedProfile = identity.settings.github.profiles.find((entry) => entry.id === profileRef) ?? null;
  const availableAgents = identity.settings.agents.filter((agent) => agent.enabled && agent.profileRef === profileRef && localAgentHasConsent(agent.kind, agent.mode));
  const selectedAgent = availableAgents.find((agent) => agent.id === agentId) ?? null;
  const r2 = identity.settings.uploads.r2;
  const nativeR2Profile = r2?.accountId && r2.publicBaseUrl
    ? { profileRef: r2.profileRef, accountId: r2.accountId, bucket: r2.bucket, publicBaseUrl: r2.publicBaseUrl, prefix: r2.prefix }
    : null;
  const selectedImageCount = selectedImages.size;
  const officialUploadsAvailable = identity.status === "authenticated" && identity.bootstrap?.publicAssetBaseUrl != null && identity.bootstrap.officialUploadOrigin != null;
  const uploadCleanupPending = pendingUploadCleanupIds.length > 0;

  useEffect(() => { titleInput.current?.focus(); }, []);
  useEffect(() => {
    if (!officialUploadsAvailable && uploadProvider === "official") setUploadProvider("r2");
  }, [officialUploadsAvailable, uploadProvider]);
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

  const close = () => { if (!busy && !uploadCleanupPending) onClose(); };
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

  const repositoryPromptFor = (agent: NonNullable<typeof selectedAgent>, repository: GitHubRepositoryRef) => agent.repositoryPrompts.find((prompt) => prompt.repository.owner.toLowerCase() === repository.owner.toLowerCase() && prompt.repository.name.toLowerCase() === repository.name.toLowerCase())?.body ?? "";
  const projectedAgentImageUrls = () => {
    const selectedDraftImages = draft.images.filter((image) => selectedImages.has(image.id));
    if (selectedImageCount === 0) return [];
    if (uploadProvider === "official" && officialUploadsAvailable) return projectedOfficialImageUrls(identity.bootstrap!.publicAssetBaseUrl!, selectedImageCount);
    if (uploadProvider === "r2" && nativeR2Profile !== null) return projectedR2ImageUrls(nativeR2Profile, draft.id, draft.revision, selectedDraftImages.map((image) => image.id));
    throw new Error("upload-profile");
  };
  const prepareAgentDraft = async () => {
    if (busy || selectedAgent?.mode !== LocalAgentMode.Draft || !selectedRepository || !selectedProfile) return;
    let normalizedDiagnostics: string | null;
    try {
      normalizedDiagnostics = diagnostics === null ? null : JSON.stringify(parseEditableBrowserDiagnostics(diagnostics));
    } catch {
      setError(copy.issueDiagnosticsInvalid);
      return;
    }
    const runId = uuidV7();
    setBusy(true); setAgentRunId(runId); setStatus(copy.localAgentPreparing); setError(null);
    try {
      const scopeId = await identity.githubPatScopeId;
      const credential = await readGitHubCredential(bridge, selectedProfile, scopeId);
      const repository = await provider.validateRepository(credential, selectedRepository);
      const response = await bridge.request({
        operation: "agent.run", runId, kind: selectedAgent.kind, mode: LocalAgentMode.Draft,
        executablePath: localAgentExecutablePath(selectedAgent.kind), repository: selectedRepository,
        private: repository.private, profileId: selectedProfile.id, scopeId,
        title: title.trim() === "" ? "" : sanitizeIssueTitle(title), body,
        labels: [...selectedLabels], diagnostics: normalizedDiagnostics, imageUrls: projectedAgentImageUrls(),
        marker: issueMarker(draft.id), repositoryPrompt: repositoryPromptFor(selectedAgent, selectedRepository),
      });
      if (response.kind !== "agent-draft") throw new Error("agent-draft");
      setTitle(response.title); setBody(response.body); setStatus(copy.localAgentDraftReady);
    } catch {
      setStatus(""); setError(copy.localAgentSubmissionFailed);
    } finally {
      setAgentRunId(null); setBusy(false);
    }
  };
  const cancelAgent = async () => {
    if (agentRunId === null) return;
    try { await bridge.request({ operation: "agent.cancel", runId: agentRunId }); }
    catch { /* The running request still owns timeout and process-tree cleanup. */ }
  };

  const submit = async () => {
    if (busy || createdUrl || uploadCleanupPending || !selectedRepository || !selectedProfile || title.trim() === "") return;
    if (!repositoryAssociations.some((entry) => repositoryKeyFor(entry) === repositoryKey && entry.profileRef === profileRef)) { setError(copy.issueRepositoryCredentialMismatch); return; }
    if (selectedImageCount > 0 && uploadProvider === "official" && !officialUploadsAvailable) { setError(copy.issueOfficialSignInRequired); return; }
    if (selectedImageCount > 0 && uploadProvider === "r2" && nativeR2Profile === null) { setError(copy.issueR2SetupRequired); return; }
    if (selectedAgent?.mode === LocalAgentMode.Direct && !window.confirm(copy.localAgentDirectConfirm)) return;
    let parsedDiagnostics = null;
    let issueTitle: string;
    try {
      issueTitle = sanitizeIssueTitle(title);
      parsedDiagnostics = diagnostics === null ? null : parseEditableBrowserDiagnostics(diagnostics);
    } catch (reason) {
      setError(reason instanceof IssueTitleInvalidError ? copy.issueTitleInvalid : copy.issueDiagnosticsInvalid);
      return;
    }
    const submissionRevision = draft.revision;
    const selectedDraftImages = draft.images.filter((image) => selectedImages.has(image.id));
    try {
      const projectedImageUrls = selectedImageCount === 0
        ? []
        : uploadProvider === "official"
          ? projectedOfficialImageUrls(identity.bootstrap!.publicAssetBaseUrl!, selectedImageCount)
          : projectedR2ImageUrls(nativeR2Profile!, draft.id, submissionRevision, selectedDraftImages.map((image) => image.id));
      composeIssueBody({ userBody: body, diagnostics: parsedDiagnostics, imageUrls: projectedImageUrls, submissionId: draft.id, diagnosticsSummary: copy.issueBrowserDiagnostics });
    } catch (reason) {
      setError(reason instanceof IssueBodyTooLargeError ? copy.issueBodyTooLarge : copy.issueSubmissionFailed);
      return;
    }
    submittedRevision.current = submissionRevision;
    setBusy(true); setError(null); setStatus(copy.issueSubmitting);
    const finalizedUploadIds: string[] = [];
    let directWriteCompleted = false;
    try {
      const scopeId = await identity.githubPatScopeId;
      const credential = await readGitHubCredential(bridge, selectedProfile, scopeId);
      const existing = await provider.searchIssueMarker(credential, selectedRepository, issueMarker(draft.id));
      if (existing.issue !== null) {
        setCreatedUrl(existing.issue.url);
        setStatus(copy.issueCreated);
        try { await onConfirmed(submissionRevision); } catch { setCleanupPending(true); setError(copy.issueDraftCleanupFailed); }
        return;
      }
      let imageUrls: readonly string[] = [];
      if (selectedImageCount > 0) {
        const flattenedResponse = await bridge.request({ operation: "capture.flatten", draftId: draft.id, expectedRevision: submissionRevision });
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
              operation: "capture.upload-official", draftId: draft.id, expectedRevision: submissionRevision, imageId: image.imageId, expectedBytes: image.bytes, expectedSha256: image.sha256, officialUploadOrigin: identity.bootstrap!.officialUploadOrigin!,
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
          const profile = nativeR2Profile!;
          imageUrls = await uploadR2Images(flattened, profile, async (image) => {
            const uploaded = await bridge.request({ operation: "capture.upload-r2", draftId: draft.id, expectedRevision: submissionRevision, imageId: image.imageId, expectedBytes: image.bytes, expectedSha256: image.sha256, profile });
            if (uploaded.kind !== "capture-uploaded" || uploaded.publicUrl === null) throw new Error("r2-upload-response");
            return uploaded.publicUrl;
          });
        }
      }
      const issueBody = composeIssueBody({ userBody: body, diagnostics: parsedDiagnostics, imageUrls, submissionId: draft.id, diagnosticsSummary: copy.issueBrowserDiagnostics });
      let issueUrl: string;
      if (selectedAgent?.mode === LocalAgentMode.Direct) {
        const repository = await provider.validateRepository(credential, selectedRepository);
        const runId = uuidV7();
        setAgentRunId(runId); setStatus(copy.localAgentDirectRunning);
        const response = await bridge.request({
          operation: "agent.run", runId, kind: selectedAgent.kind, mode: LocalAgentMode.Direct,
          executablePath: localAgentExecutablePath(selectedAgent.kind), repository: selectedRepository,
          private: repository.private, profileId: selectedProfile.id, scopeId,
          title: issueTitle, body: issueBody, labels: [...selectedLabels],
          diagnostics: parsedDiagnostics === null ? null : JSON.stringify(parsedDiagnostics),
          imageUrls, marker: issueMarker(draft.id), repositoryPrompt: repositoryPromptFor(selectedAgent, selectedRepository),
        });
        directWriteCompleted = true;
        if (response.kind !== "agent-direct") throw new Error("agent-direct");
        const reconciled = await provider.searchIssueMarker(credential, selectedRepository, issueMarker(draft.id));
        if (reconciled.issue === null || reconciled.issue.url !== response.issueUrl) throw new Error("agent-reconciliation");
        issueUrl = response.issueUrl;
      } else {
        const result = await provider.createIssue(credential, selectedRepository, { title: issueTitle, body: stripFinalSubmissionMarker(issueBody, draft.id), labels: [...selectedLabels], submissionId: draft.id });
        issueUrl = result.issue.url;
      }
      setCreatedUrl(issueUrl);
      setStatus(copy.issueCreated);
      try { await onConfirmed(submissionRevision); } catch { setCleanupPending(true); setError(copy.issueDraftCleanupFailed); }
    } catch (reason) {
      const ambiguous = directWriteCompleted
        || reason instanceof NativeBridgeError && reason.code === NativeBridgeErrorCode.AgentWriteAmbiguous
        || reason instanceof GitHubProviderError && reason.code === GitHubErrorCode.AmbiguousWrite;
      const remainingCleanupIds = ambiguous ? [] : await deleteFinalizedUploads(finalizedUploadIds, (input) => deleteUpload.mutateAsync(input));
      setPendingUploadCleanupIds(remainingCleanupIds);
      setError(remainingCleanupIds.length > 0 ? copy.issueUploadCleanupFailed : ambiguous ? copy.issueAmbiguous : copy.issueSubmissionFailed);
      setStatus("");
    } finally { setAgentRunId(null); setBusy(false); }
  };

  const retryUploadCleanup = async () => {
    if (busy || pendingUploadCleanupIds.length === 0) return;
    setBusy(true); setError(null); setStatus(copy.issueCleaningUploads);
    const remainingCleanupIds = await deleteFinalizedUploads(pendingUploadCleanupIds, (input) => deleteUpload.mutateAsync(input));
    setPendingUploadCleanupIds(remainingCleanupIds);
    setStatus("");
    setError(remainingCleanupIds.length > 0 ? copy.issueUploadCleanupFailed : copy.issueSubmissionFailed);
    setBusy(false);
  };

  const retryCleanup = async () => {
    setBusy(true); setError(null);
    try {
      if (submittedRevision.current === null) throw new Error("submission-revision");
      await onConfirmed(submittedRevision.current); setCleanupPending(false); onClose();
    }
    catch { setError(copy.issueDraftCleanupFailed); }
    finally { setBusy(false); }
  };

  return <div className="overlay" role="presentation"><section ref={dialog} className="issue-dialog" role="dialog" aria-modal="true" aria-labelledby="issue-dialog-title" onKeyDown={keyDown}>
    <h3 id="issue-dialog-title">{copy.issueModalTitle}</h3>
    <label>{copy.issueRepository}<select value={repositoryKey} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => { const association = repositoryAssociations.find((entry) => repositoryKeyFor(entry) === event.target.value); setRepositoryKey(event.target.value); if (association) setProfileRef(association.profileRef); }}><option value="">{copy.issueSelectRepository}</option>{repositories.map((repository) => <option key={repositoryKeyFor(repository)} value={repositoryKeyFor(repository)}>{repository.owner}/{repository.name}</option>)}</select></label>
    <label>{copy.issueCredential}<select value={profileRef} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => { setProfileRef(event.target.value); setAgentId(""); }}><option value="">{copy.githubSelectProfile}</option>{identity.settings.github.profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>
    <label>{copy.localAgentSubmission}<select value={selectedAgent?.id ?? ""} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => setAgentId(event.target.value)}><option value="">{copy.localAgentManual}</option>{availableAgents.map((agent) => <option key={agent.id} value={agent.id}>{agent.kind} — {agent.mode === LocalAgentMode.Draft ? copy.localAgentDraftMode : copy.localAgentDirectMode}</option>)}</select></label>
    <label>{copy.issueTitle}<input ref={titleInput} autoFocus value={title} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => setTitle(event.target.value)} /></label>
    <label>{copy.issueBody}<textarea value={body} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => setBody(event.target.value)} /></label>
    {selectedAgent?.mode === LocalAgentMode.Draft && <button type="button" disabled={busy || !!createdUrl || uploadCleanupPending || !selectedRepository || !selectedProfile} onClick={() => void prepareAgentDraft()}>{copy.localAgentPrepareDraft}</button>}
    <fieldset><legend>{copy.issueImages}</legend>{draft.images.map((image, index) => <label className="check" key={image.id}><input type="checkbox" checked={selectedImages.has(image.id)} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={() => toggleImage(image.id)} /><img src={image.previewUrl} alt="" />{copy.editorImage} {index + 1}</label>)}</fieldset>
    {diagnostics !== null && <div className="issue-diagnostics"><label>{copy.issueBrowserDiagnostics}<textarea value={diagnostics} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => setDiagnostics(event.target.value)} /></label><button type="button" disabled={busy || !!createdUrl || uploadCleanupPending} onClick={() => setDiagnostics(null)}>{copy.issueRemoveDiagnostics}</button></div>}
    <fieldset><legend>{copy.issueLabels}</legend>{labels.length === 0 ? <p>{copy.issueNoLabels}</p> : labels.map((label) => <label className="check" key={label}><input type="checkbox" checked={selectedLabels.has(label)} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={() => toggleLabel(label)} />{label}</label>)}</fieldset>
    <label>{copy.issueUploadProvider}<select value={uploadProvider} disabled={busy || !!createdUrl || uploadCleanupPending} onChange={(event) => setUploadProvider(event.target.value as "official" | "r2")}><option value="official" disabled={!officialUploadsAvailable}>{copy.issueUploadOfficial}</option><option value="r2">{copy.issueUploadR2}</option></select></label>
    <dl><dt>{copy.issueSubmissionPath}</dt><dd>{selectedAgent === null ? copy.issueSubmissionPathDirect : selectedAgent.mode === LocalAgentMode.Draft ? copy.localAgentDraftMode : copy.localAgentDirectMode}</dd></dl>
    {selectedImageCount > 0 && !officialUploadsAvailable && <p className="notice">{copy.issueOfficialSignInRequired}</p>}
    {selectedImageCount > 0 && <p className="notice">{copy.issuePublicImageWarning}</p>}
    {status && <p role="status" aria-live="polite">{status}</p>}{error && <p role="alert" className="native-setting-error">{error}</p>}
    {createdUrl && <p><a href={createdUrl} target="_blank" rel="noreferrer">{createdUrl}</a></p>}
    <div className="actions">{agentRunId && <button type="button" onClick={() => void cancelAgent()}>{copy.localAgentCancel}</button>}{uploadCleanupPending ? <button className="primary" disabled={busy} onClick={() => void retryUploadCleanup()}>{copy.issueRetryUploadCleanup}</button> : cleanupPending ? <button className="primary" disabled={busy} onClick={() => void retryCleanup()}>{copy.issueRetryDraftCleanup}</button> : <button className="primary" disabled={busy || !!createdUrl || !selectedRepository || !selectedProfile || title.trim() === ""} onClick={() => void submit()}>{copy.issueSubmit}</button>}<button disabled={busy || uploadCleanupPending} onClick={close}>{copy.close}</button></div>
  </section></div>;
}

type SubmissionRepository = GitHubRepositoryRef & { readonly profileRef: string };
function submissionRepositories(settings: ReturnType<typeof useIdentitySettings>["settings"]): readonly SubmissionRepository[] {
  const entries: SubmissionRepository[] = settings.github.repositories.flatMap((entry) => entry.profileRef ? [{ owner: entry.owner, name: entry.name, profileRef: entry.profileRef }] : []);
  const tracker = settings.github.issueTracker;
  if (tracker?.profileRef) entries.unshift({ owner: tracker.owner, name: tracker.repository, profileRef: tracker.profileRef });
  return [...new Map(entries.map((entry) => [`${repositoryKeyFor(entry)}\0${entry.profileRef}`, entry])).values()];
}
function uniqueSubmissionRepositories(associations: readonly SubmissionRepository[]): readonly GitHubRepositoryRef[] { return [...new Map(associations.map((entry) => [repositoryKeyFor(entry), { owner: entry.owner, name: entry.name }])).values()]; }
function repositoryKeyFor(repository: GitHubRepositoryRef): string { return `${repository.owner.toLowerCase()}/${repository.name.toLowerCase()}`; }
function uuid(value: string): { readonly value: string } { return { value }; }

async function deleteFinalizedUploads(uploadIds: readonly string[], remove: (input: { readonly uploadId: { readonly value: string } }) => Promise<unknown>): Promise<readonly string[]> {
  const results = await Promise.allSettled(uploadIds.map((uploadId) => remove({ uploadId: uuid(uploadId) })));
  return uploadIds.filter((_uploadId, index) => results[index]?.status === "rejected");
}
