import { describe, expect, it } from "vitest";
import { MaximumJsonBytes, createBoundedRequest } from "./protocol.js";

describe("Native Messaging request bounds", () => {
  it("drops optional sanitized markup before posting an oversized capture", () => {
    const bounded = createBoundedRequest("capture", {
      mappingId: "01900000-0000-7000-8000-000000000001",
      context: { title: "safe", outerHtml: "x".repeat(MaximumJsonBytes) },
    });
    expect(bounded.withinLimit).toBe(true);
    expect((bounded.request.payload as { context: { outerHtml: string } }).context.outerHtml).toBe("");
  });

  it("rejects an outbound request that still exceeds the shared ceiling", () => {
    expect(createBoundedRequest("ping", { value: "x".repeat(MaximumJsonBytes) }).withinLimit).toBe(false);
  });
});
