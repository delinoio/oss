import { Code, ConnectError } from "@connectrpc/connect";
import {
  ErrorDetailSchema,
  ErrorReason,
} from "@delinoio/delibase-connect";
import { describe, expect, it } from "vitest";

import {
  describeDelibaseError,
  isPermissionError,
} from "../api/errors";

function delibaseError(reason: ErrorReason, code = Code.FailedPrecondition) {
  return new ConnectError("safe server message", code, undefined, [
    {
      desc: ErrorDetailSchema,
      value: { reason },
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
});
