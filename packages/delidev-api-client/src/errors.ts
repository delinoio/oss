import { Code, ConnectError } from "@connectrpc/connect";
import { ErrorDetailSchema } from "./gen/delidev/v1/worker_pb.js";
import { isEntityId } from "./validation.js";

export enum FailureCode {
  InvalidArgument = "invalid_argument",
  NotFound = "not_found",
  Conflict = "conflict",
  Unauthenticated = "unauthenticated",
  PermissionDenied = "permission_denied",
  Unavailable = "unavailable",
  ServerUnavailable = "server_unavailable",
  ConfirmationRequired = "confirmation_required",
  MissingInput = "missing_input",
  Unsupported = "unsupported",
  RecoveryRequired = "recovery_required",
  BudgetReached = "budget_reached",
  ResourceExhausted = "resource_exhausted",
  CursorExpired = "cursor_expired",
  ProviderDisabled = "provider_disabled",
  Canceled = "canceled",
  Internal = "internal",
}

export enum FailureCause {
  QuotaExhausted = "quota_exhausted",
  ConfigurationNameConflict = "configuration_name_conflict",
  // Check-only Tailscale probe causes proven by the server (see the connections
  // contract). They never carry hostnames, URLs or native error text.
  TailscaleConnectionRefused = "tailscale_connection_refused",
  TailscaleTimeout = "tailscale_timeout",
  TailscaleCertificateInvalid = "tailscale_certificate_invalid",
  TailscaleUnexpectedHttp = "tailscale_unexpected_http",
  TailscaleInvalidIdentity = "tailscale_invalid_identity",
}

// Explicit closed code/cause pairs. Any other combination projects to undefined.
const failureCausePairs: ReadonlyArray<readonly [FailureCode, FailureCause]> = [
  [FailureCode.MissingInput, FailureCause.QuotaExhausted],
  [FailureCode.Conflict, FailureCause.ConfigurationNameConflict],
  [FailureCode.Unavailable, FailureCause.TailscaleConnectionRefused],
  [FailureCode.Unavailable, FailureCause.TailscaleTimeout],
  [FailureCode.Unavailable, FailureCause.TailscaleCertificateInvalid],
  [FailureCode.Unavailable, FailureCause.TailscaleUnexpectedHttp],
  [FailureCode.Unsupported, FailureCause.TailscaleInvalidIdentity],
];

/** Closed code/cause projection shared by RPC and retained Session problems. */
export function failureCause(
  code: unknown,
  cause: unknown,
): FailureCause | undefined {
  for (const [pairCode, pairCause] of failureCausePairs)
    if (code === pairCode && cause === pairCause) return pairCause;
  return undefined;
}

export interface ClientFailure {
  cause?: FailureCause;
  code: FailureCode;
  message: string;
  guidance: string;
  correlationId?: string;
}

export function clientFailure(reason: unknown): ClientFailure {
  const error = ConnectError.from(reason);
  const detail = error.findDetails(ErrorDetailSchema)[0];
  if (
    detail &&
    Object.values(FailureCode).includes(detail.code as FailureCode)
  ) {
    return {
      code: detail.code as FailureCode,
      cause: failureCause(detail.code, detail.cause),
      message: error.rawMessage,
      guidance: detail.guidance,
      correlationId: isEntityId(detail.correlationId)
        ? detail.correlationId
        : undefined,
    };
  }
  const code =
    (
      {
        [Code.Unauthenticated]: FailureCode.Unauthenticated,
        [Code.PermissionDenied]: FailureCode.PermissionDenied,
        [Code.Canceled]: FailureCode.Canceled,
        [Code.NotFound]: FailureCode.NotFound,
        [Code.Aborted]: FailureCode.Conflict,
        [Code.InvalidArgument]: FailureCode.InvalidArgument,
        [Code.ResourceExhausted]: FailureCode.ResourceExhausted,
        [Code.Unimplemented]: FailureCode.Unsupported,
        [Code.FailedPrecondition]: FailureCode.RecoveryRequired,
      } as Partial<Record<Code, FailureCode>>
    )[error.code] ?? FailureCode.ServerUnavailable;
  // Browser/proxy/network errors can carry URLs and arbitrary response text.
  // Only the versioned server error detail permits product error presentation.
  return {
    code,
    message: "The DeliDev request could not complete.",
    guidance:
      "Check the selected connection and inspect accepted work before retrying the same request.",
  };
}
