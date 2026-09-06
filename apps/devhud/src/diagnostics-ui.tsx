import { DiagnosticsQuery, QuotaKind, StaticCapability, mapDevHudError, type DevHudClientError } from "@delinoio/devhud-api-client";
import { useMutation } from "@connectrpc/connect-query";
import { useEffect, useRef, useState } from "react";
import { diagnosticsConsentDigest, prepareDiagnosticsBundle, readDiagnosticEvents, type PreparedDiagnosticsBundle } from "./diagnostics";
import { useIdentitySettings, type IdentityStatus } from "./service-boundary";
import type { Copy } from "./localization";
import type { NativeBridgeV1, RuntimeSnapshot } from "./native-bridge";
import { Button, Card, DataRow, StatePanel, StatusBadge, type StatusTone } from "./ui-foundation";

interface DiagnosticsPanelProps {
  readonly copy: Copy;
  readonly runtime: RuntimeSnapshot;
  readonly bridge: NativeBridgeV1;
  readonly storage: Storage;
  readonly online: boolean;
}

type ExportState = "idle" | "saved" | "cancelled" | "initiated" | "failed";

export function DiagnosticsPanel({ copy, runtime, bridge, storage, online }: DiagnosticsPanelProps) {
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
  const deletionPending = identity.status === "deletion-pending";
  const blocked = identity.status === "blocked";
  const crashReportsSupported = identity.bootstrap?.capabilities.includes(StaticCapability.CRASH_REPORTS) === true;
  const submissionBlock = diagnosticsSubmissionBlock(identity.status, online, consentDigest !== null, crashReportsSupported);
  const crashReportQuota = submitError?.kind === "quotaExceeded" && submitError.detail.quota === QuotaKind.CRASH_REPORTS
    ? submitError.detail
    : null;

  useEffect(() => {
    if (identity.status !== "deletion-pending" && identity.status !== "signed-out") return;
    consentAttempt.current += 1;
    exportAttempt.current += 1;
    setBundle(null);
    setPreviewRequested(false);
    setConsentSelected(false);
    setConsentDigest(null);
    setExportState("idle");
    setSubmitState("idle");
    setSubmitError(null);
    setServerCorrelation(null);
  }, [identity.status]);

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
    if (!bundle || identity.status === "deletion-pending" || exportPendingRef.current) return;
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

  const exportCopy = exportState === "idle" ? null : copy[`diagnosticsExport${capitalize(exportState)}` as keyof Copy];
  const exportTone: StatusTone = exportState === "saved" ? "success" : exportState === "failed" ? "danger" : "info";

  return <section className="diagnostics-panel">
    <Card className="diagnostics-runtime" aria-label={copy.diagnosticsRuntime}>
      <DataRow title={copy.diagnosticPlatform} description={runtime.operatingSystem} />
      <DataRow title={copy.diagnosticArchitecture} description={runtime.architecture} />
      <DataRow title={copy.diagnosticBridge} description={`v${runtime.bridgeVersion}`} />
    </Card>
    <Card className="diagnostics-privacy">
      <StatusBadge tone="info">{copy.diagnosticsPrivacy}</StatusBadge>
      <p>{copy.diagnosticsRetention}</p>
    </Card>
    <div className="diagnostics-preview-action"><Button variant="primary" onClick={preview}>{copy.diagnosticsPreview}</Button></div>
    {bundle === null ? previewRequested && <StatePanel eyebrow={copy.empty} title={copy.diagnosticsNoEventsTitle} summary={copy.diagnosticsNoEvents} tone="neutral" /> : <>
      <section className="diagnostics-disclosures" aria-label={copy.diagnosticsDisclosures}>
        <details className="diagnostics-disclosure" open>
          <summary>{copy.diagnosticsExactPayload}</summary>
          <pre className="diagnostics-preview" data-testid="diagnostics-preview" tabIndex={0}>{bundle.requestJson}</pre>
        </details>
        <details className="diagnostics-disclosure">
          <summary>{copy.diagnosticsExactExport}</summary>
          <pre className="diagnostics-preview" data-testid="diagnostics-export-preview" tabIndex={0}>{bundle.exportJson}</pre>
        </details>
      </section>
      {!deletionPending && <div className="diagnostics-actions"><Button disabled={exportPending} onClick={() => void exportBundle()}>{copy.diagnosticsExport}</Button></div>}
      {exportCopy && <StatePanel eyebrow={copy.diagnostics} title={copy.diagnosticsExport} summary={exportCopy} tone={exportTone} />}
      {deletionPending
        ? <StatePanel eyebrow={copy.blocked} title={copy.diagnosticsDeletionPendingTitle} summary={copy.diagnosticsDeletionPending} role="alert" tone="danger" />
        : blocked
          ? <StatePanel eyebrow={copy.blocked} title={copy.diagnosticsBlockedTitle} summary={copy.diagnosticsBlocked} role="alert" tone="danger" />
          : !authenticated
            ? <StatePanel eyebrow={copy.diagnostics} title={copy.diagnosticsGuestTitle} summary={copy.diagnosticsGuestNoSubmit} tone="info" />
            : !crashReportsSupported
              ? <StatePanel eyebrow={copy.unavailable} title={copy.diagnosticsUnsupportedTitle} summary={copy.diagnosticsUnsupported} tone="warning" />
              : !online
                ? <StatePanel eyebrow={copy.offline} title={copy.diagnosticsOfflineTitle} summary={copy.diagnosticsOffline} tone="warning" />
                : <div className="diagnostics-submission">
                  <label className="check"><input type="checkbox" checked={consentSelected} onChange={(event) => void chooseConsent(event.target.checked)} />{copy.diagnosticsConsent}</label>
                  <Button variant="primary" disabled={consentDigest === null || submit.isPending} onClick={() => void submitBundle()}>{copy.diagnosticsSubmit}</Button>
                </div>}
      {submitState === "sent" && <StatePanel eyebrow={copy.diagnosticsSent} title={copy.diagnosticsSubmit} summary={<>{copy.diagnosticsSent} <code>{serverCorrelation}</code></>} tone="success" />}
      {submitState === "failed" && <StatePanel eyebrow={copy.error} title={copy.diagnosticsSubmit} role="alert" tone={crashReportQuota ? "warning" : "danger"} summary={crashReportQuota
        ? <>{copy.diagnosticsSubmitQuotaExceeded} {copy.diagnosticsSubmitQuotaLimit}: <code>{crashReportQuota.limit.toString()}</code></>
        : submitError?.kind === "permissionDenied" || submitError?.kind === "unauthenticated"
          ? copy.diagnosticsSubmitDenied
          : copy.diagnosticsSubmitFailed} details={submitError && <p className="correlation"><code>{`diagnostics-connect-${submitError.code}`}</code>{submitError.correlationId && <> {copy.correlationId}: <code>{submitError.correlationId}</code></>}</p>} />}
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
