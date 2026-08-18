import { DiagnosticsQuery, StaticCapability, mapDevHudError, type DevHudClientError } from "@delinoio/devhud-api-client";
import { useMutation } from "@connectrpc/connect-query";
import { useRef, useState } from "react";
import { diagnosticsConsentDigest, prepareDiagnosticsBundle, readDiagnosticEvents, type PreparedDiagnosticsBundle } from "./diagnostics";
import { useIdentitySettings, type IdentityStatus } from "./service-boundary";
import type { Copy } from "./localization";
import type { NativeBridgeV1, RuntimeSnapshot } from "./native-bridge";

interface DiagnosticsPanelProps {
  readonly copy: Copy;
  readonly runtime: RuntimeSnapshot;
  readonly bridge: NativeBridgeV1;
  readonly storage: Storage;
  readonly online: boolean;
}

type ExportState = "idle" | "saved" | "cancelled" | "initiated" | "failed";

export function DiagnosticsPanel({ copy, bridge, storage, online }: DiagnosticsPanelProps) {
  const identity = useIdentitySettings();
  const submit = useMutation(DiagnosticsQuery.submitCrashReport);
  const [bundle, setBundle] = useState<PreparedDiagnosticsBundle | null>(null);
  const [previewRequested, setPreviewRequested] = useState(false);
  const [consentSelected, setConsentSelected] = useState(false);
  const [consentDigest, setConsentDigest] = useState<string | null>(null);
  const [exportState, setExportState] = useState<ExportState>("idle");
  const [submitState, setSubmitState] = useState<"idle" | "sent" | "failed">("idle");
  const [submitError, setSubmitError] = useState<DevHudClientError | null>(null);
  const [serverCorrelation, setServerCorrelation] = useState<string | null>(null);
  const [exportPending, setExportPending] = useState(false);
  const consentAttempt = useRef(0);
  const exportAttempt = useRef(0);
  const exportPendingRef = useRef(false);
  const authenticated = identity.status === "authenticated";
  const blocked = identity.status === "blocked" || identity.status === "deletion-pending";
  const crashReportsSupported = identity.bootstrap?.capabilities.includes(StaticCapability.CRASH_REPORTS) === true;
  const submissionBlock = diagnosticsSubmissionBlock(identity.status, online, consentDigest !== null, crashReportsSupported);

  const preview = () => {
    const events = readDiagnosticEvents(storage);
    const latest = events.at(-1);
    consentAttempt.current += 1;
    exportAttempt.current += 1;
    setConsentSelected(false);
    setConsentDigest(null);
    setExportState("idle");
    setSubmitState("idle");
    setSubmitError(null);
    setServerCorrelation(null);
    setPreviewRequested(true);
    setBundle(latest ? prepareDiagnosticsBundle(latest, events) : null);
  };

  const chooseConsent = async (checked: boolean) => {
    const attempt = ++consentAttempt.current;
    setConsentSelected(checked);
    setConsentDigest(null);
    if (!checked || !bundle) return;
    try {
      const digest = await diagnosticsConsentDigest(bundle.requestJson);
      if (consentAttempt.current === attempt) setConsentDigest(digest);
    } catch {
      if (consentAttempt.current === attempt) setConsentSelected(false);
    }
  };

  const exportBundle = async () => {
    if (!bundle || exportPendingRef.current) return;
    exportPendingRef.current = true;
    setExportPending(true);
    const attempt = ++exportAttempt.current;
    setExportState("idle");
    try {
      const response = await bridge.request({ operation: "diagnostics.export", suggestedName: `devhud-diagnostics-${bundle.correlationId}.json`, contents: bundle.exportJson });
      if (response.kind !== "diagnostics-export") throw new Error("diagnostics-export-failed");
      if (exportAttempt.current === attempt) setExportState(response.outcome);
    } catch {
      if (exportAttempt.current === attempt) setExportState("failed");
    } finally {
      exportPendingRef.current = false;
      setExportPending(false);
    }
  };

  const submitBundle = async () => {
    if (!bundle || submissionBlock !== null || consentDigest === null) return;
    const attempt = consentAttempt.current;
    setSubmitError(null);
    const verifiedDigest = await diagnosticsConsentDigest(bundle.requestJson);
    if (consentAttempt.current !== attempt) return;
    if (verifiedDigest !== consentDigest) {
      setConsentSelected(false);
      setConsentDigest(null);
      setSubmitState("failed");
      return;
    }
    try {
      const response = await submit.mutateAsync(bundle.request);
      if (consentAttempt.current !== attempt) return;
      setServerCorrelation(response.metadata?.correlationId?.value ?? null);
      setSubmitState("sent");
      setConsentSelected(false);
      setConsentDigest(null);
    } catch (reason) {
      if (consentAttempt.current !== attempt) return;
      setSubmitError(mapDevHudError(reason));
      setSubmitState("failed");
      setConsentSelected(false);
      setConsentDigest(null);
    }
  };

  return <section className="diagnostics-panel">
    <p>{copy.diagnosticsRetention}</p>
    <button className="primary" onClick={preview}>{copy.diagnosticsPreview}</button>
    {bundle === null ? previewRequested && <p role="status">{copy.diagnosticsNoEvents}</p> : <>
      <p>{copy.diagnosticsExactPayload}</p>
      <pre className="diagnostics-preview" data-testid="diagnostics-preview">{bundle.requestJson}</pre>
      <p>{copy.diagnosticsExactExport}</p>
      <pre className="diagnostics-preview" data-testid="diagnostics-export-preview">{bundle.exportJson}</pre>
      <div className="actions"><button disabled={exportPending} onClick={() => void exportBundle()}>{copy.diagnosticsExport}</button></div>
      {exportState !== "idle" && <p role="status">{copy[`diagnosticsExport${capitalize(exportState)}` as keyof Copy]}</p>}
      {authenticated && !blocked && crashReportsSupported && <>
        <label className="check"><input type="checkbox" checked={consentSelected} onChange={(event) => void chooseConsent(event.target.checked)} />{copy.diagnosticsConsent}</label>
        <button className="primary" disabled={!online || consentDigest === null || submit.isPending} onClick={() => void submitBundle()}>{copy.diagnosticsSubmit}</button>
      </>}
      {!authenticated && !blocked && <p className="notice">{copy.diagnosticsGuestNoSubmit}</p>}
      {blocked && <p className="notice" role="alert">{copy.diagnosticsBlocked}</p>}
      {authenticated && !online && <p className="notice">{copy.diagnosticsOffline}</p>}
      {submitState === "sent" && <p role="status">{copy.diagnosticsSent} {serverCorrelation}</p>}
      {submitState === "failed" && <p role="alert">
        {submitError?.kind === "permissionDenied" || submitError?.kind === "unauthenticated" ? copy.diagnosticsSubmitDenied : copy.diagnosticsSubmitFailed}
        {submitError && <> <code>{`diagnostics-connect-${submitError.code}`}</code>{submitError.correlationId && <> {copy.correlationId}: <code>{submitError.correlationId}</code></>}</>}
      </p>}
    </>}
  </section>;
}

export type DiagnosticsSubmissionBlock = "guest" | "blocked" | "unsupported" | "offline" | "consent-required";

export function diagnosticsSubmissionBlock(status: IdentityStatus, online: boolean, consented: boolean, supported: boolean): DiagnosticsSubmissionBlock | null {
  if (status === "blocked" || status === "deletion-pending") return "blocked";
  if (status !== "authenticated") return "guest";
  if (!supported) return "unsupported";
  if (!online) return "offline";
  if (!consented) return "consent-required";
  return null;
}

function capitalize(value: ExportState): string {
  return value[0]!.toUpperCase() + value.slice(1);
}
