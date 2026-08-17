import { describe, expect, it, vi } from "vitest";
import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, type NativeBridgeV1 } from "./native-bridge";
import { profileRequiresSetup } from "./profile-secrets";

function bridgeWith(request: NativeBridgeV1["request"]): NativeBridgeV1 {
  return { request, async listen() { return () => {}; } };
}

describe("profile secret boundary", () => {
  it("requires setup for the selected missing GitHub profile without trying another profile", async () => {
    const request = vi.fn(async () => ({ kind: "secure-value" as const, value: null }));
    expect(await profileRequiresSetup(bridgeWith(request), "github", "selected")).toBe(true);
    expect(request).toHaveBeenCalledExactlyOnceWith({ operation: "secure.read", setting: { kind: SecureSettingKind.GithubPat, profileId: "selected" } });
  });

  it("requires both selected R2 credentials and fails closed when secure storage is unavailable", async () => {
    const missingSecret = bridgeWith(async (request) => ({ kind: "secure-value", value: request.operation === "secure.read" && request.setting.kind === SecureSettingKind.R2AccessKeyId ? "present" : null }));
    expect(await profileRequiresSetup(missingSecret, "r2", "r2-profile")).toBe(true);

    const unavailable = bridgeWith(async () => { throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure); });
    expect(await profileRequiresSetup(unavailable, "github", "selected")).toBe(true);
  });

  it("accepts a profile only when every contracted secret exists", async () => {
    const bridge = bridgeWith(async () => ({ kind: "secure-value", value: "present" }));
    expect(await profileRequiresSetup(bridge, "r2", "complete")).toBe(false);
  });
});
