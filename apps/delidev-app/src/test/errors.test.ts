import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  DeletionBlockerKind,
  DeletionBlockerSchema,
  ErrorDetailSchema,
  ErrorReason,
  type DeletionBlocker,
} from "@delinoio/delibase-connect";
import { describe, expect, it } from "vitest";

import {
  describeDelibaseError,
  isPermissionError,
} from "../api/errors";

function delibaseError(
  reason: ErrorReason,
  code = Code.FailedPrecondition,
  deletionBlockers: DeletionBlocker[] = [],
) {
  return new ConnectError("safe server message", code, undefined, [
    {
      desc: ErrorDetailSchema,
      value: { deletionBlockers, reason },
    },
  ]);
}

describe("delibase error details", () => {
  it("renders actionable last-Owner and reservation blockers", () => {
    expect(
      describeDelibaseError(delibaseError(ErrorReason.LAST_OWNER_BLOCKER)),
    ).toContain("another member to Owner");
    expect(
      describeDelibaseError(
        delibaseError(ErrorReason.TEAM_SUBTREE_HAS_ACTIVE_RESERVATIONS),
      ),
    ).toContain("active usage reservations");
  });

  it("uses stable authorization details instead of UI role guesses", () => {
    expect(
      isPermissionError(
        delibaseError(ErrorReason.OWNER_ROLE_REQUIRED, Code.PermissionDenied),
      ),
    ).toBe(true);
    expect(
      describeDelibaseError(
        delibaseError(
          ErrorReason.OWNER_ROLE_REQUIRED,
          Code.PermissionDenied,
        ),
      ),
    ).toBe("Only an organization Owner can perform this action.");
  });

  it("uses the account deletion blocker's actionable guidance", () => {
    expect(
      describeDelibaseError(
        delibaseError(
          ErrorReason.ACCOUNT_DELETION_BLOCKED,
          Code.FailedPrecondition,
          [
            create(DeletionBlockerSchema, {
              kind: DeletionBlockerKind.ACTIVE_USAGE_RESERVATION,
            }),
          ],
        ),
      ),
    ).toContain("Settle or release");
    expect(
      describeDelibaseError(
        delibaseError(
          ErrorReason.ACCOUNT_DELETION_BLOCKED,
          Code.FailedPrecondition,
          [
            create(DeletionBlockerSchema, {
              kind: DeletionBlockerKind.LAST_ORGANIZATION_OWNER,
            }),
            create(DeletionBlockerSchema, {
              kind: DeletionBlockerKind.ACTIVE_USAGE_RESERVATION,
            }),
          ],
        ),
      ),
    ).toContain("Transfer ownership");
  });

  it("maps billing and reservation details to actionable states", () => {
    expect(
      describeDelibaseError(
        delibaseError(ErrorReason.OVERAGE_LIMIT_EXHAUSTED),
      ),
    ).toContain("Increase the limit");
    expect(
      describeDelibaseError(
        delibaseError(ErrorReason.SUBSCRIPTION_PAST_DUE),
      ),
    ).toContain("billing portal");
    expect(
      describeDelibaseError(delibaseError(ErrorReason.RESERVATION_EXPIRED)),
    ).toContain("held funds were released");
    expect(
      describeDelibaseError(
        delibaseError(ErrorReason.COMMIT_UNITS_EXCEED_RESERVED),
      ),
    ).toContain("reserved units");
  });

  it("maps background authorization conflict and access-loss recovery", () => {
    expect(
      describeDelibaseError(
        delibaseError(
          ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
        ),
      ),
    ).toContain("Restore access");
    expect(
      describeDelibaseError(
        delibaseError(ErrorReason.BACKGROUND_USAGE_REPLAY_CONFLICT),
      ),
    ).toContain("does not match the original");
    expect(
      describeDelibaseError(
        delibaseError(
          ErrorReason.BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
        ),
      ),
    ).toContain("exact resource");
  });

  it("prefers actionable code guidance over generic server diagnostics", () => {
    expect(
      describeDelibaseError(
        new ConnectError("request failed", Code.Unavailable),
      ),
    ).toContain("temporarily unavailable");
    expect(
      describeDelibaseError(
        new ConnectError("request failed", Code.Unauthenticated),
      ),
    ).toContain("Sign in again");
  });
});
