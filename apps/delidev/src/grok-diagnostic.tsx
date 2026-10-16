// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import {
  FailureCode,
  GrokDiagnosticPhase,
  GrokDiagnosticSchema,
  isEntityId,
  type GrokDiagnostic,
} from "@delinoio/delidev-api-client";
import { copy, useLocale, type MessageKey } from "./localization";
import { object, text } from "./documents";
import { validInstallationVersion, validSemVer } from "./harness-version";
import { InlineRemediation } from "./ui";

/** Stable phase identifiers shared by the wire enum and Worker documents. */
enum GrokPhaseName {
  Discovery = "discovery",
  Version = "version",
  Profile = "profile",
  Runtime = "runtime",
  Login = "login",
  Refresh = "refresh",
  Logout = "logout",
  Models = "models",
  Execution = "execution",
  History = "history",
  Cleanup = "cleanup",
}

const wirePhases: Record<GrokPhaseName, GrokDiagnosticPhase> = {
  [GrokPhaseName.Discovery]: GrokDiagnosticPhase.DISCOVERY,
  [GrokPhaseName.Version]: GrokDiagnosticPhase.VERSION,
  [GrokPhaseName.Profile]: GrokDiagnosticPhase.PROFILE,
  [GrokPhaseName.Runtime]: GrokDiagnosticPhase.RUNTIME,
  [GrokPhaseName.Login]: GrokDiagnosticPhase.LOGIN,
  [GrokPhaseName.Refresh]: GrokDiagnosticPhase.REFRESH,
  [GrokPhaseName.Logout]: GrokDiagnosticPhase.LOGOUT,
  [GrokPhaseName.Models]: GrokDiagnosticPhase.MODELS,
  [GrokPhaseName.Execution]: GrokDiagnosticPhase.EXECUTION,
  [GrokPhaseName.History]: GrokDiagnosticPhase.HISTORY,
  [GrokPhaseName.Cleanup]: GrokDiagnosticPhase.CLEANUP,
};

const phaseKeys: Partial<Record<GrokDiagnosticPhase, MessageKey>> = {
  [GrokDiagnosticPhase.DISCOVERY]: "grok-diagnostic.phase.discovery",
  [GrokDiagnosticPhase.VERSION]: "grok-diagnostic.phase.version",
  [GrokDiagnosticPhase.PROFILE]: "grok-diagnostic.phase.profile",
  [GrokDiagnosticPhase.RUNTIME]: "grok-diagnostic.phase.runtime",
  [GrokDiagnosticPhase.LOGIN]: "grok-diagnostic.phase.login",
  [GrokDiagnosticPhase.REFRESH]: "grok-diagnostic.phase.refresh",
  [GrokDiagnosticPhase.LOGOUT]: "grok-diagnostic.phase.logout",
  [GrokDiagnosticPhase.MODELS]: "grok-diagnostic.phase.models",
  [GrokDiagnosticPhase.EXECUTION]: "grok-diagnostic.phase.execution",
  [GrokDiagnosticPhase.HISTORY]: "grok-diagnostic.phase.history",
  [GrokDiagnosticPhase.CLEANUP]: "grok-diagnostic.phase.cleanup",
};

// These server-authored English step names recognize the original timeout
// message independently of the UI language. They never authorize a retry.
const wireStepNames: Partial<Record<GrokDiagnosticPhase, string>> = {
  [GrokDiagnosticPhase.DISCOVERY]: "executable discovery",
  [GrokDiagnosticPhase.VERSION]: "version validation",
  [GrokDiagnosticPhase.PROFILE]: "profile validation",
  [GrokDiagnosticPhase.RUNTIME]: "runtime preparation",
  [GrokDiagnosticPhase.LOGIN]: "sign-in",
  [GrokDiagnosticPhase.REFRESH]: "credential refresh",
  [GrokDiagnosticPhase.LOGOUT]: "sign-out",
  [GrokDiagnosticPhase.MODELS]: "model discovery",
  [GrokDiagnosticPhase.EXECUTION]: "execution",
  [GrokDiagnosticPhase.HISTORY]: "history verification",
  [GrokDiagnosticPhase.CLEANUP]: "cleanup",
};

// Every code with a dedicated explanation. Go's GrokDiagnostic.text owns the
// same table; the other valid codes fall back to the generic reason.
const reasonKeys: Partial<Record<FailureCode, MessageKey>> = {
  [FailureCode.InvalidArgument]: "grok-diagnostic.reason.invalidArgument",
  [FailureCode.NotFound]: "grok-diagnostic.reason.notFound",
  [FailureCode.Conflict]: "grok-diagnostic.reason.conflict",
  [FailureCode.Unsupported]: "grok-diagnostic.reason.unsupported",
  [FailureCode.Unavailable]: "grok-diagnostic.reason.unavailable",
  [FailureCode.ServerUnavailable]: "grok-diagnostic.reason.serverUnavailable",
  [FailureCode.Unauthenticated]: "grok-diagnostic.reason.unauthenticated",
  [FailureCode.PermissionDenied]: "grok-diagnostic.reason.permissionDenied",
  [FailureCode.RecoveryRequired]: "grok-diagnostic.reason.recoveryRequired",
  [FailureCode.ResourceExhausted]: "grok-diagnostic.reason.resourceExhausted",
  [FailureCode.Canceled]: "grok-diagnostic.reason.canceled",
  [FailureCode.Internal]: "grok-diagnostic.reason.internal",
};

// Phases that act on the local Grok Build installation or its private runtime.
// Only an access failure in one of them is told to restore OS permissions.
const installationPhases: readonly GrokDiagnosticPhase[] = [
  GrokDiagnosticPhase.DISCOVERY,
  GrokDiagnosticPhase.VERSION,
  GrokDiagnosticPhase.PROFILE,
  GrokDiagnosticPhase.RUNTIME,
  GrokDiagnosticPhase.CLEANUP,
];

/**
 * Choose the guidance for one closed (phase, code) pair:
 * - permission_denied in an installation phase: restore OS permission;
 * - any other discovery, version or profile failure: install the supported
 *   release (no account execution was authorized);
 * - every other failure, including runtime and cleanup failures that are not
 *   access related (invalid home, unexpected profile entry, symlink, invalid
 *   inherited environment, launch or initialization): keep the account and
 *   operation and check the installation.
 */
export function grokGuidanceKey(
  phase: GrokDiagnosticPhase,
  code: string,
): MessageKey {
  if (
    code === FailureCode.PermissionDenied &&
    installationPhases.includes(phase)
  )
    return "grok-diagnostic.guidance.access";
  if (
    phase === GrokDiagnosticPhase.DISCOVERY ||
    phase === GrokDiagnosticPhase.VERSION ||
    phase === GrokDiagnosticPhase.PROFILE
  )
    return "grok-diagnostic.guidance.install";
  return "grok-diagnostic.guidance.operation";
}

export interface SafeGrokDiagnostic {
  version: string;
  supported: string;
  phase: string;
  code: string;
  correlation: string;
  message: string;
  guidance: string;
}

/**
 * Reconstruct presentation from closed metadata. Server message and guidance
 * strings, native text, paths and URLs never reach the screen.
 */
export function safeGrokDiagnostic(
  d?: GrokDiagnostic,
): SafeGrokDiagnostic | undefined {
  if (
    !d ||
    (d.detectedVersion && !validInstallationVersion(d.detectedVersion)) ||
    // The server always reports the supported baseline (domain.GrokDiagnostic.
    // Validate), so an omitted or malformed value means the metadata is untrusted.
    !validInstallationVersion(d.supportedVersion) ||
    !validSemVer(d.supportedVersion) ||
    !phaseKeys[d.phase] ||
    !Object.values(FailureCode).includes(d.code as FailureCode)
  )
    return undefined;
  const supported = d.supportedVersion;
  const timeout =
    d.code === FailureCode.Unavailable &&
    d.message ===
      `Grok Build ${d.detectedVersion || "not detected"} did not complete ${wireStepNames[d.phase]}. The native operation timed out.`;
  let reason: string;
  if (timeout) reason = copy("grok-diagnostic.reason.timeout");
  else if (d.phase === GrokDiagnosticPhase.VERSION)
    reason = validSemVer(d.detectedVersion)
      ? copy("grok-diagnostic.reason.versionSupported", { supported })
      : copy("grok-diagnostic.reason.versionInvalid");
  else
    reason = copy(
      reasonKeys[d.code as FailureCode] ?? "grok-diagnostic.reason.other",
    );
  const step = copy(phaseKeys[d.phase]!);
  const guidanceKey = grokGuidanceKey(d.phase, d.code);
  return {
    version: d.detectedVersion,
    supported,
    phase: step,
    code: d.code,
    correlation: isEntityId(d.correlationId) ? d.correlationId : "",
    message: copy("grok-diagnostic.message", {
      version: d.detectedVersion || copy("grok-diagnostic.notDetected"),
      step: step.toLowerCase(),
      reason,
    }),
    guidance: copy(guidanceKey),
  };
}

/**
 * Read the closed Grok diagnostic that discovery stores on an installation
 * observation. Unknown phases or malformed values are dropped, never guessed.
 */
export function grokDiagnosticFromDocument(
  value: unknown,
): GrokDiagnostic | undefined {
  const source = object(value);
  const phase = wirePhases[text(source.phase) as GrokPhaseName];
  if (!phase) return undefined;
  return create(GrokDiagnosticSchema, {
    detectedVersion: text(source.detected_version),
    supportedVersion: text(source.supported_version),
    phase,
    code: text(source.code),
    message: text(source.message),
    guidance: text(source.guidance),
    correlationId: text(source.correlation_id),
  });
}

/**
 * Presentation only: the owning workflow supplies original actions. Used by
 * Settings > AI Subscription and by the Worker installation row.
 */
export enum GrokDiagnosticOwner {
  Server = "server",
  Worker = "worker",
}

export function GrokDiagnosticNotice({
  diagnostic,
  actions,
  owner = GrokDiagnosticOwner.Worker,
}: {
  diagnostic?: GrokDiagnostic;
  actions?: ReactNode;
  owner?: GrokDiagnosticOwner;
}) {
  useLocale();
  const safe = safeGrokDiagnostic(diagnostic);
  const alive = useRef(true);
  const [copied, setCopied] = useState<boolean | undefined>();
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  if (!safe) return null;
  const notReported = copy("grok-diagnostic.label.notReported");
  return (
    <InlineRemediation
      summary={
        <>
          <p>{safe.message}</p>
          <p>
            {owner === GrokDiagnosticOwner.Server
              ? copy("grok-diagnostic.guidance.server")
              : safe.guidance}
          </p>
        </>
      }
      actions={actions}
      details={
        <>
          <dl>
            <div>
              <dt>{copy("grok-diagnostic.label.detectedVersion")}</dt>
              <dd>{safe.version || copy("grok-diagnostic.notDetected")}</dd>
            </div>
            <div>
              <dt>{copy("grok-diagnostic.label.supportedVersion")}</dt>
              <dd>{safe.supported || notReported}</dd>
            </div>
            <div>
              <dt>{copy("grok-diagnostic.label.failedStep")}</dt>
              <dd>{safe.phase}</dd>
            </div>
            <div>
              <dt>{copy("grok-diagnostic.label.errorCode")}</dt>
              <dd>{safe.code}</dd>
            </div>
            <div>
              <dt>{copy("grok-diagnostic.label.reference")}</dt>
              <dd>{safe.correlation || notReported}</dd>
            </div>
          </dl>
          {safe.correlation ? (
            <button
              type="button"
              onClick={() => {
                setCopied(undefined);
                if (!navigator.clipboard) {
                  setCopied(false);
                  return;
                }
                void navigator.clipboard.writeText(safe.correlation).then(
                  () => {
                    if (alive.current) setCopied(true);
                  },
                  () => {
                    if (alive.current) setCopied(false);
                  },
                );
              }}
            >
              {copy("grok-diagnostic.copyReference")}
            </button>
          ) : null}
          {copied === true ? (
            <p role="status">{copy("grok-diagnostic.copied")}</p>
          ) : null}
          {copied === false ? (
            <p role="status">{copy("grok-diagnostic.copyFailed")}</p>
          ) : null}
        </>
      }
    />
  );
}
