import { describe, expect, it } from "vitest";

import {
  DEVHUD_CORRELATION_ID_HEADER,
  getDevHudCorrelationId,
} from "../src/correlation.js";

describe("response correlation metadata", () => {
  it("accepts a UUID v7 response header", () => {
    const headers = new Headers({
      [DEVHUD_CORRELATION_ID_HEADER]: "0198B8D0-07D0-7C4D-8D61-4F2019A76506",
    });

    expect(getDevHudCorrelationId(headers)).toBe(
      "0198b8d0-07d0-7c4d-8d61-4f2019a76506",
    );
  });

  it("rejects missing, malformed, and non-v7 values", () => {
    expect(getDevHudCorrelationId(new Headers())).toBeUndefined();
    expect(
      getDevHudCorrelationId(
        new Headers({ [DEVHUD_CORRELATION_ID_HEADER]: "not-a-uuid" }),
      ),
    ).toBeUndefined();
    expect(
      getDevHudCorrelationId(
        new Headers({
          [DEVHUD_CORRELATION_ID_HEADER]: "0198b8d0-07d0-6c4d-8d61-4f2019a76506",
        }),
      ),
    ).toBeUndefined();
  });
});
