import { Code, ConnectError } from "@connectrpc/connect";
import {
  ErrorDetailSchema,
  ErrorReason,
  type Revision,
} from "@delinoio/devhud-realqa-connect/devhud-realqa/v1/common_pb";

export interface RealQAError {
  code: Code;
  message: string;
  reason: ErrorReason;
  currentRevision?: Revision;
}

const reasonMessages = new Map<ErrorReason, string>([
  [
    ErrorReason.AUTHENTICATION_REQUIRED,
    "Your session is no longer valid. Sign in again and retry.",
  ],
  [
    ErrorReason.REAUTHENTICATION_REQUIRED,
    "GitHub authorization expired or was revoked. Connect GitHub again.",
  ],
  [
    ErrorReason.PERMISSION_DENIED,
    "You do not have permission to manage this RealQA destination.",
  ],
  [
    ErrorReason.OWNER_SCOPE_NOT_FOUND,
    "This DeliDev owner is no longer available.",
  ],
  [
    ErrorReason.STALE_REVISION,
    "The GitHub connection changed. Refresh its status before retrying.",
  ],
  [
    ErrorReason.IDEMPOTENCY_CONFLICT,
    "This safe retry no longer matches the original disconnect request. Refresh before retrying.",
  ],
  [
    ErrorReason.GITHUB_DISCONNECTED,
    "GitHub is disconnected or temporarily unavailable. Connect again or retry.",
  ],
  [
    ErrorReason.UNSUPPORTED_TRACKER_HOST,
    "Only GitHub.com is supported. GitHub Enterprise Server and custom hosts are not available.",
  ],
  [
    ErrorReason.PROVIDER_PERMISSION_DENIED,
    "Your GitHub account cannot submit issues to this repository.",
  ],
  [
    ErrorReason.PROVIDER_SCHEMA_INVALID,
    "GitHub issue templates and forms could not be loaded for this repository.",
  ],
  [
    ErrorReason.PROVIDER_VALIDATION_FAILED,
    "GitHub rejected this repository request. Refresh the destination and retry.",
  ],
  [
    ErrorReason.RATE_LIMITED,
    "GitHub is temporarily rate limited. Retry later.",
  ],
]);

export function getRealQAError(error: unknown): RealQAError {
  const connectError = ConnectError.from(error);
  const detail = connectError.findDetails(ErrorDetailSchema)[0];
  const reason = detail?.reason ?? ErrorReason.UNSPECIFIED;
  const codeMessage =
    connectError.code === Code.Unavailable
      ? "RealQA is temporarily unavailable. Check your connection and retry; safe retries keep the same operation identity."
      : connectError.code === Code.Unauthenticated
        ? "Your session is no longer valid. Sign in again and retry."
        : undefined;
  return {
    code: connectError.code,
    currentRevision: detail?.currentRevision,
    message:
      (detail && reasonMessages.get(detail.reason)) ||
      detail?.message ||
      codeMessage ||
      connectError.rawMessage ||
      "The RealQA request could not be completed. Please try again.",
    reason,
  };
}

export function describeRealQAError(error: unknown): string {
  return getRealQAError(error).message;
}
