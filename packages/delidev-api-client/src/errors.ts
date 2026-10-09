import { Code, ConnectError } from "@connectrpc/connect";
import { ErrorDetailSchema } from "./gen/delidev/v1/common_pb.js";
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

export interface ClientFailure {
  code: FailureCode;
  message: string;
  guidance: string;
  correlationId?: string;
}

export function clientFailure(reason: unknown): ClientFailure {
  const error = ConnectError.from(reason);
  const detail = error.findDetails(ErrorDetailSchema)[0];
  if (detail && Object.values(FailureCode).includes(detail.code as FailureCode)) {
    return {
      code: detail.code as FailureCode,
      message: error.rawMessage,
      guidance: detail.guidance,
      correlationId: isEntityId(detail.correlationId) ? detail.correlationId : undefined,
    };
  }
  const code = ({
    [Code.Unauthenticated]: FailureCode.Unauthenticated,
    [Code.PermissionDenied]: FailureCode.PermissionDenied,
    [Code.Canceled]: FailureCode.Canceled,
    [Code.NotFound]: FailureCode.NotFound,
    [Code.Aborted]: FailureCode.Conflict,
    [Code.InvalidArgument]: FailureCode.InvalidArgument,
    [Code.ResourceExhausted]: FailureCode.ResourceExhausted,
    [Code.Unimplemented]: FailureCode.Unsupported,
    [Code.FailedPrecondition]: FailureCode.RecoveryRequired,
  } as Partial<Record<Code, FailureCode>>)[error.code] ?? FailureCode.ServerUnavailable;
  // Browser/proxy/network errors can carry URLs and arbitrary response text.
  // Only the versioned server error detail permits product error presentation.
  return { code, message: "The DeliDev request could not complete.", guidance: "Check the selected connection and inspect accepted work before retrying the same request." };
}
