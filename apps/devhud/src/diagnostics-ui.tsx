import { DiagnosticsQuery, mapDevHudError, type DevHudClientError } from "@delinoio/devhud-api-client";
import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
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

type ExportState = "idle" | "saved" | "cancelled" | "failed";

export function DiagnosticsPanel({ copy, bridge, storage, online }: DiagnosticsPanelProps) {
  const identity = useIdentitySettings();
  const submit = useMutation(DiagnosticsQuery.submitCrashReport);
  const [bundle, setBundle] = useState<PreparedDiagnosticsBundle | null>(null);
  const [consentDigest, setConsentDigest] = useState<string | null>(null);
  const [exportState, setExportState] = useState<ExportState>("idle");
  const [submitState, setSubmitState] = useState<"idle" | "sent" | "failed">("idle");
  const [submitError, setSubmitError] = useState<DevHudClientError | null>(null);
  const [serverCorrelation, setServerCorrelation] = useState<string | null>(null);
  const authenticated = identity.status === "authenticated";
  const blocked = identity.status === "blocked" || identity.status === "deletion-pending";
  const submissionBlock = diagnosticsSubmissionBlock(identity.status, online, consentDigest !== null);

  const preview = () => {
    const events = readDiagnosticEvents(storage);
    const latest = events.at(-1);
    setConsentDigest(null);
    setSubmitState("idle");
    setSubmitError(null);
    setServerCorrelation(null);
    setBundle(latest ? prepareDiagnosticsBundle(latest, events) : null);
  };

  const chooseConsent = async (checked: boolean) => {
    setConsentDigest(checked && bundle ? await diagnosticsConsentDigest(bundle.requestJson) : null);
  };

  const exportBundle = async () => {
    if (!bundle) return;
    setExportState("idle");
    try {
      const response = await bridge.request({ operation: "diagnostics.export", suggestedName: `devhud-diagnostics-${bundle.correlationId}.json`, contents: bundle.exportJson });
      if (response.kind !== "diagnostics-export") throw new Error("diagnostics-export-failed");
      setExportState(response.outcome);
    } catch {
      setExportState("failed");
    }
  };

  const submitBundle = async () => {
    if (!bundle || submissionBlock !== null || consentDigest === null) return;
    setSubmitError(null);
    if (await diagnosticsConsentDigest(bundle.requestJson) !== consentDigest) {
      setConsentDigest(null);
      setSubmitState("failed");
      return;
    }
    try {
      const response = await submit.mutateAsync(bundle.request);
      setServerCorrelation(response.metadata?.correlationId?.value ?? null);
      setSubmitState("sent");
      setConsentDigest(null);
    } catch (reason) {
      setSubmitError(mapDevHudError(reason));
      setSubmitState("failed");
      setConsentDigest(null);
    }
  };

  return <section className="diagnostics-panel">
    <p>{copy.diagnosticsRetention}</p>
    <button className="primary" onClick={preview}>{copy.diagnosticsPreview}</button>
    {bundle === null ? <p role="status">{copy.diagnosticsNoEvents}</p> : <>
      <p>{copy.diagnosticsExactPayload}</p>
      <pre className="diagnostics-preview" data-testid="diagnostics-preview">{bundle.requestJson}</pre>
      <p>{copy.diagnosticsExactExport}</p>
      <pre className="diagnostics-preview" data-testid="diagnostics-export-preview">{bundle.exportJson}</pre>
      <div className="actions"><button onClick={() => void exportBundle()}>{copy.diagnosticsExport}</button></div>
      {exportState !== "idle" && <p role="status">{copy[`diagnosticsExport${capitalize(exportState)}` as keyof Copy]}</p>}
      {authenticated && !blocked && <>
        <label className="check"><input type="checkbox" checked={consentDigest !== null} onChange={(event) => void chooseConsent(event.target.checked)} />{copy.diagnosticsConsent}</label>
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

export type DiagnosticsSubmissionBlock = "guest" | "blocked" | "offline" | "consent-required";

export function diagnosticsSubmissionBlock(status: IdentityStatus, online: boolean, consented: boolean): DiagnosticsSubmissionBlock | null {
  if (status === "blocked" || status === "deletion-pending") return "blocked";
  if (status !== "authenticated") return "guest";
  if (!online) return "offline";
  if (!consented) return "consent-required";
  return null;
}

function capitalize(value: ExportState): string {
  return value[0]!.toUpperCase() + value.slice(1);
}
