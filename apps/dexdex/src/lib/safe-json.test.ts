import { describe, expect, it } from "vitest";
import { stringifyForUi } from "./safe-json";

describe("stringifyForUi", () => {
  it("serializes bigint fields without throwing", () => {
    const rendered = stringifyForUi({
      sequence: 42n,
      nested: {
        value: 9n,
      },
    });

    expect(rendered).toContain("\"42\"");
    expect(rendered).toContain("\"9\"");
  });

  it("returns fallback text when serialization fails", () => {
    const circular: { self?: unknown } = {};
    circular.self = circular;

    expect(stringifyForUi(circular)).toContain("failed to serialize value");
  });
});
