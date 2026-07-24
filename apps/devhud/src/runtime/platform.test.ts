import { describe, expect, it } from "vitest";

import { detectApplicationPlatform, platformForRuntime } from "./platform";

describe("application platform detection", () => {
  it.each([
    ["Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)", "mobile"],
    ["Mozilla/5.0 (Linux; Android 14; Pixel 8)", "mobile"],
    ["Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)", "desktop"],
  ] as const)("selects the %s shell", (userAgent, platform) => {
    expect(detectApplicationPlatform(userAgent)).toBe(platform);
  });

  it.each([
    ["cef", "desktop"],
    ["system-webview", "mobile"],
  ] as const)("uses the %s runtime as the authoritative platform signal", (runtime, platform) => {
    expect(platformForRuntime(runtime)).toBe(platform);
  });
});
