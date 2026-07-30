import { Code, ConnectError } from "@connectrpc/connect";
import {
  ErrorDetailSchema,
  ErrorReason,
} from "@delinoio/devhud-deck-connect/devhud-deck/v1/common_pb";
import { describe, expect, it } from "vitest";

import { getDeckError } from "../deck/errors";

function deckError(reason: ErrorReason, code = Code.FailedPrecondition) {
  return new ConnectError("safe server message", code, undefined, [
    {
      desc: ErrorDetailSchema,
      value: { reason },
    },
  ]);
}

describe("Deck error details", () => {
  it("distinguishes revision, GitHub permission, GHES, and dependency states", () => {
    expect(getDeckError(deckError(ErrorReason.STALE_REVISION)).message).toContain(
      "changed since it was loaded",
    );
    expect(
      getDeckError(deckError(ErrorReason.GITHUB_PERMISSION_DENIED)).message,
    ).toContain("GitHub permissions");
    expect(
      getDeckError(deckError(ErrorReason.UNSUPPORTED_GITHUB_HOST)).message,
    ).toContain("GitHub Enterprise Server");
    expect(
      getDeckError(deckError(ErrorReason.DEPENDENCY_UNAVAILABLE)).message,
    ).toContain("temporarily unavailable");
  });

  it("uses a safe network state instead of raw diagnostics", () => {
    const described = getDeckError(
      new ConnectError(
        "upstream contained sensitive diagnostics",
        Code.Unavailable,
      ),
    ).message;
    expect(described).toContain("temporarily unavailable");
    expect(described).not.toContain("sensitive diagnostics");
  });
});
