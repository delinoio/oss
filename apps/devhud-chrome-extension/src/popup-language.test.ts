import { describe, expect, it } from "vitest";
import { resolveExtensionLanguage } from "./popup-language.js";

describe("popup language", () => {
  it("marks Korean Chrome UI copy as Korean", () => {
    expect(resolveExtensionLanguage("ko-KR")).toBe("ko");
    expect(resolveExtensionLanguage("en-US")).toBe("en");
  });
});
