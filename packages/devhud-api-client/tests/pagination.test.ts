import { describe, expect, it } from "vitest";

import {
  DEFAULT_PAGE_SIZE,
  MAX_PAGE_SIZE,
  MAX_PAGE_TOKEN_BYTES,
  createPageRequest,
} from "../src/pagination.js";

describe("bounded opaque pagination", () => {
  it("uses the contracted default and preserves opaque tokens", () => {
    const token = "opaque.query-and-owner-scoped.value";
    expect(createPageRequest(0, token)).toMatchObject({
      pageSize: DEFAULT_PAGE_SIZE,
      pageToken: token,
    });
  });

  it("accepts the maximum page size and rejects larger pages", () => {
    expect(createPageRequest(MAX_PAGE_SIZE).pageSize).toBe(100);
    expect(() => createPageRequest(MAX_PAGE_SIZE + 1)).toThrow(RangeError);
    expect(() => createPageRequest(-1)).toThrow(RangeError);
    expect(() => createPageRequest(1.5)).toThrow(RangeError);
  });

  it("bounds tokens by UTF-8 byte length", () => {
    expect(createPageRequest(50, "x".repeat(MAX_PAGE_TOKEN_BYTES)).pageToken).toHaveLength(
      MAX_PAGE_TOKEN_BYTES,
    );
    expect(() => createPageRequest(50, "한".repeat(MAX_PAGE_TOKEN_BYTES))).toThrow(RangeError);
  });

  it("rejects malformed Unicode without changing valid opaque tokens", () => {
    const validToken = "opaque-😀-token";
    expect(createPageRequest(50, validToken).pageToken).toBe(validToken);

    for (const malformedToken of ["\ud800", "\udc00", "opaque-\ud800-token"]) {
      expect(() => createPageRequest(50, malformedToken)).toThrow(TypeError);
    }
  });
});
