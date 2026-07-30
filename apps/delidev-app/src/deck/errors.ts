import { Code, ConnectError } from "@connectrpc/connect";
import {
  ErrorDetailSchema,
  ErrorReason,
} from "@delinoio/devhud-deck-connect/devhud-deck/v1/common_pb";

export interface DeckError {
  code: Code;
  message: string;
  reason: ErrorReason;
}
const reasonMessages = new Map<ErrorReason, string>([
  [
    ErrorReason.AUTHENTICATION_REQUIRED,
    "Your Deck session is no longer valid. Sign in again and retry.",
  ],
  [
    ErrorReason.INVALID_CREDENTIALS,
    "Your Deck authorization could not be verified. Sign in again and retry.",
  ],
  [
    ErrorReason.SUBJECT_MISMATCH,
    "The Deck and DeliDev sessions do not identify the same account. Sign in again.",
  ],
  [
    ErrorReason.PERMISSION_DENIED,
    "You do not have permission to manage this GitHub connection.",
  ],
  [
    ErrorReason.GITHUB_PERMISSION_DENIED,
    "GitHub permissions no longer allow this connection. Review the GitHub.com permissions and retry.",
  ],
  [
    ErrorReason.STALE_REVISION,
    "This connection changed since it was loaded. Its current state must be reviewed before retrying.",
  ],
  [
    ErrorReason.UNSUPPORTED_GITHUB_HOST,
    "Deck supports GitHub.com only. GitHub Enterprise Server and custom GitHub hosts are not supported.",
  ],
  [
    ErrorReason.DEPENDENCY_UNAVAILABLE,
    "Deck or GitHub is temporarily unavailable. Reconnect and retry with the same pending action.",
  ],
  [
    ErrorReason.DELETION_IN_PROGRESS,
    "Deck data deletion is in progress for this owner.",
  ],
]);

export function getDeckError(error: unknown): DeckError {
  const connectError = ConnectError.from(error);
  const detail = connectError.findDetails(ErrorDetailSchema)[0];
  const reason = detail?.reason ?? ErrorReason.UNSPECIFIED;
  const codeMessage =
    connectError.code === Code.Unavailable
      ? "Deck is temporarily unavailable. Check your connection and retry with the same pending action."
      : connectError.code === Code.Unauthenticated
        ? "Your Deck session is no longer valid. Sign in again and retry."
        : connectError.code === Code.PermissionDenied
          ? "You do not have permission to manage this GitHub connection."
          : connectError.code === Code.NotFound
            ? "No GitHub connection exists for this owner."
            : undefined;
  return {
    code: connectError.code,
    message:
      reasonMessages.get(reason) ||
      detail?.message ||
      codeMessage ||
      "The GitHub connection request could not be completed. Please retry.",
    reason,
  };
}

export function isDeckNotFound(error: unknown): boolean {
  const deckError = getDeckError(error);
  return (
    deckError.code === Code.NotFound ||
    deckError.reason === ErrorReason.NOT_FOUND
  );
}
