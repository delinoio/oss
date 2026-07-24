import { Code, ConnectError } from "@connectrpc/connect";
import {
  ErrorDetailSchema,
  ErrorReason,
  type DeletionBlocker,
} from "@delinoio/delibase-connect";

export interface DelibaseError {
  blockers: DeletionBlocker[];
  code: Code;
  message: string;
  reason: ErrorReason;
}

const reasonMessages = new Map<ErrorReason, string>([
  [
    ErrorReason.PERMISSION_DENIED,
    "You do not have permission to perform this action.",
  ],
  [
    ErrorReason.OWNER_ROLE_REQUIRED,
    "Only an organization Owner can perform this action.",
  ],
  [
    ErrorReason.ADMIN_ROLE_REQUIRED,
    "Only an organization Owner or Admin can perform this action.",
  ],
  [
    ErrorReason.SLUG_CONFLICT,
    "That organization URL is already in use. Choose another slug.",
  ],
  [
    ErrorReason.SLUG_INVALID,
    "Use lowercase letters, numbers, and single hyphens for the slug.",
  ],
  [
    ErrorReason.LAST_OWNER_BLOCKER,
    "This would leave the organization without an Owner. Promote another member to Owner first.",
  ],
  [
    ErrorReason.MEMBER_HAS_ACTIVE_RESERVATIONS,
    "This member still has active usage reservations. Settle or release them before removing access.",
  ],
  [
    ErrorReason.INVITATION_INVALID,
    "This invitation link is invalid or has already been used by this account.",
  ],
  [
    ErrorReason.INVITATION_EXPIRED,
    "This invitation expired. Ask an organization Owner or Admin for a new link.",
  ],
  [
    ErrorReason.INVITATION_REVOKED,
    "This invitation was revoked by the organization.",
  ],
  [
    ErrorReason.INVITATION_TEAM_REQUIRED,
    "Member invitations must assign a target team and team role.",
  ],
  [
    ErrorReason.GENERAL_TEAM_PROTECTED,
    "The protected General team cannot be renamed, moved, or deleted.",
  ],
  [
    ErrorReason.TEAM_DEPTH_EXCEEDED,
    "That change would exceed the five-level team hierarchy limit.",
  ],
  [
    ErrorReason.TEAM_CYCLE,
    "A team cannot be moved beneath itself or one of its descendants.",
  ],
  [
    ErrorReason.TEAM_SUBTREE_HAS_ACTIVE_RESERVATIONS,
    "This subtree has active usage reservations. Settle or release them before deleting it.",
  ],
  [
    ErrorReason.ORGANIZATION_DELETION_BLOCKED,
    "This organization has active usage reservations and cannot be deleted yet.",
  ],
  [
    ErrorReason.ACCOUNT_DELETION_BLOCKED,
    "Account deletion is blocked until every organization has another Owner.",
  ],
]);

export function getDelibaseError(error: unknown): DelibaseError {
  const connectError = ConnectError.from(error);
  const detail = connectError.findDetails(ErrorDetailSchema)[0];
  return {
    blockers: detail?.deletionBlockers ?? [],
    code: connectError.code,
    message:
      (detail && reasonMessages.get(detail.reason)) ||
      detail?.message ||
      connectError.rawMessage ||
      "The request could not be completed. Please try again.",
    reason: detail?.reason ?? ErrorReason.UNSPECIFIED,
  };
}

export function describeDelibaseError(error: unknown): string {
  return getDelibaseError(error).message;
}

export function isPermissionError(error: unknown): boolean {
  const { code, reason } = getDelibaseError(error);
  return (
    code === Code.PermissionDenied ||
    reason === ErrorReason.PERMISSION_DENIED ||
    reason === ErrorReason.OWNER_ROLE_REQUIRED ||
    reason === ErrorReason.ADMIN_ROLE_REQUIRED ||
    reason === ErrorReason.ORGANIZATION_MEMBERSHIP_REQUIRED ||
    reason === ErrorReason.TEAM_ACCESS_DENIED
  );
}
