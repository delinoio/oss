import { Code, ConnectError } from "@connectrpc/connect";
import {
  DeletionBlockerKind,
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
    "Account deletion is blocked. Transfer ownership where you are the last Owner and settle or release active usage reservations.",
  ],
  [
    ErrorReason.SUBSCRIPTION_INACTIVE,
    "Overage requires an active subscription. Existing credit remains available.",
  ],
  [
    ErrorReason.SUBSCRIPTION_PAST_DUE,
    "Payment is past due. Use the billing portal to restore billing; existing credit remains available, but new overage is blocked.",
  ],
  [
    ErrorReason.SUBSCRIPTION_CANCELED,
    "The subscription is canceled. Existing credit remains available, but new overage is blocked.",
  ],
  [
    ErrorReason.SUBSCRIPTION_REVOKED,
    "The subscription was revoked. Existing credit remains available, but new overage is blocked.",
  ],
  [
    ErrorReason.OVERAGE_NOT_CONFIGURED,
    "Monthly overage defaults to zero. An organization Owner or Admin must set a non-negative limit before overage can be used.",
  ],
  [
    ErrorReason.OVERAGE_DISABLED,
    "New overage is disabled. Check the subscription state and monthly overage limit.",
  ],
  [
    ErrorReason.OVERAGE_LIMIT_EXHAUSTED,
    "Committed and held overage have reached the monthly limit. Increase the limit or wait for the next billing period.",
  ],
  [
    ErrorReason.AVAILABLE_FUNDS_EXHAUSTED,
    "Available credit and permitted overage cannot cover this reservation. Release unused holds, increase the limit, or wait for more credit.",
  ],
  [
    ErrorReason.PRICE_UNAVAILABLE,
    "This meter does not have an available price. Choose another app or try again later.",
  ],
  [
    ErrorReason.MONEY_OVERFLOW,
    "The requested amount is outside the supported billing range. Reduce it and try again.",
  ],
  [
    ErrorReason.USAGE_UNITS_INVALID,
    "The requested usage units are invalid. Check the amount and try again.",
  ],
  [
    ErrorReason.USAGE_UNITS_OVERFLOW,
    "The requested usage units are too large. Reduce the amount and try again.",
  ],
  [
    ErrorReason.CATALOG_PRECISION_INVALID,
    "This meter has invalid billing precision and cannot be used. Contact support.",
  ],
  [
    ErrorReason.OVERAGE_LIMIT_INVALID,
    "Enter a non-negative monthly overage limit with no more than six decimal places.",
  ],
  [
    ErrorReason.RESERVATION_NOT_FOUND,
    "This usage reservation no longer exists. Start the operation again.",
  ],
  [
    ErrorReason.RESERVATION_EXPIRED,
    "This usage reservation expired and its held funds were released. Start the operation again.",
  ],
  [
    ErrorReason.RESERVATION_ALREADY_COMMITTED,
    "This usage reservation was already committed. Refresh usage to see the result.",
  ],
  [
    ErrorReason.RESERVATION_ALREADY_RELEASED,
    "This usage reservation was already released. Start a new operation if usage is still needed.",
  ],
  [
    ErrorReason.RESERVATION_FINALIZED,
    "This usage reservation is already finalized and cannot be changed.",
  ],
  [
    ErrorReason.COMMIT_UNITS_EXCEED_RESERVED,
    "Committed usage cannot exceed the reserved units. Start a larger reservation first.",
  ],
  [
    ErrorReason.CLIENT_REFERENCE_CONFLICT,
    "That usage reference conflicts with an earlier operation. Use a new reference or replay the original request.",
  ],
  [
    ErrorReason.RESERVATION_UNITS_NEGATIVE,
    "Reservation units must be zero or greater.",
  ],
  [
    ErrorReason.IDEMPOTENCY_KEY_REQUIRED,
    "A safe retry key is required. Retry the action.",
  ],
  [
    ErrorReason.IDEMPOTENCY_CONFLICT,
    "This retry key was already used with different input. Refresh the page before trying again.",
  ],
  [
    ErrorReason.IDEMPOTENCY_OPERATION_MISMATCH,
    "This retry key belongs to a different operation. Refresh the page before trying again.",
  ],
  [
    ErrorReason.RESOURCE_CONFLICT,
    "This resource changed or an operation is already in progress. Refresh the page and try again.",
  ],
  [
    ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
    "The authorization binding does not match this resource, service, meter, purpose, owner, team, or period. Refresh and rebind the exact resource before retrying.",
  ],
  [
    ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
    "The authorizer no longer has the required organization or team access. Restore access, then create a new authorization.",
  ],
  [
    ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_STATUS_INVALID,
    "This authorization is no longer active. Review its status and rebind with a new authorization if usage should resume.",
  ],
  [
    ErrorReason.BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
    "Current-period usage has reached this authorization’s maximum units. Wait for the next UTC period or rebind with an appropriate limit.",
  ],
  [
    ErrorReason.BACKGROUND_USAGE_REPLAY_CONFLICT,
    "This retry does not match the original background-usage operation. Refresh before starting a new action.",
  ],
]);

function accountDeletionBlockerMessage(
  blockers: DeletionBlocker[],
): string | undefined {
  const hasLastOwnerBlocker = blockers.some(
    (blocker) =>
      blocker.kind === DeletionBlockerKind.LAST_ORGANIZATION_OWNER,
  );
  const hasReservationBlocker = blockers.some(
    (blocker) =>
      blocker.kind === DeletionBlockerKind.ACTIVE_USAGE_RESERVATION,
  );
  if (hasLastOwnerBlocker && hasReservationBlocker) {
    return "Account deletion is blocked. Transfer ownership where you are the last Owner and settle or release active usage reservations.";
  }
  if (hasReservationBlocker) {
    return "Account deletion is blocked by active usage reservations. Settle or release them before deleting your account.";
  }
  if (hasLastOwnerBlocker) {
    return "Account deletion is blocked until every organization has another Owner.";
  }
  return undefined;
}

export function getDelibaseError(error: unknown): DelibaseError {
  const connectError = ConnectError.from(error);
  const detail = connectError.findDetails(ErrorDetailSchema)[0];
  const blockers = detail?.deletionBlockers ?? [];
  const reason = detail?.reason ?? ErrorReason.UNSPECIFIED;
  const codeMessage =
    connectError.code === Code.Unavailable
      ? "The service is temporarily unavailable. Check your connection and retry; safe retries keep the same operation identity."
      : connectError.code === Code.Unauthenticated
        ? "Your session is no longer valid. Sign in again and retry."
        : undefined;
  return {
    blockers,
    code: connectError.code,
    message:
      (reason === ErrorReason.ACCOUNT_DELETION_BLOCKED &&
        accountDeletionBlockerMessage(blockers)) ||
      (detail && reasonMessages.get(detail.reason)) ||
      detail?.message ||
      codeMessage ||
      connectError.rawMessage ||
      "The request could not be completed. Please try again.",
    reason,
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
