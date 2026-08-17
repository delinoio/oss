import { NativeBridgeError, SecureSettingKind, type NativeBridgeV1 } from "./native-bridge";

export async function profileRequiresSetup(bridge: NativeBridgeV1, kind: "github" | "r2", profileId: string): Promise<boolean> {
  const refs = kind === "github"
    ? [{ kind: SecureSettingKind.GithubPat, profileId }]
    : [{ kind: SecureSettingKind.R2AccessKeyId, profileId }, { kind: SecureSettingKind.R2SecretAccessKey, profileId }];
  try {
    const responses = await Promise.all(refs.map((setting) => bridge.request({ operation: "secure.read", setting })));
    return responses.some((response) => response.kind !== "secure-value" || response.value === null);
  } catch (reason) {
    if (reason instanceof NativeBridgeError) return true;
    throw reason;
  }
}
