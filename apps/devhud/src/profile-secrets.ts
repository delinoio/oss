import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, type NativeBridgeV1, type SecureSettingRef } from "./native-bridge";

export function profileRequiresSetup(bridge: NativeBridgeV1, kind: "github", profileId: string, githubPatScopeId: string): Promise<boolean>;
export function profileRequiresSetup(bridge: NativeBridgeV1, kind: "r2", profileId: string): Promise<boolean>;
export async function profileRequiresSetup(bridge: NativeBridgeV1, kind: "github" | "r2", profileId: string, githubPatScopeId?: string): Promise<boolean> {
  let refs: readonly SecureSettingRef[];
  if (kind === "github") {
    if (githubPatScopeId === undefined) throw new NativeBridgeError(NativeBridgeErrorCode.InvalidArgument);
    refs = [{ kind: SecureSettingKind.GithubPat, profileId, scopeId: githubPatScopeId }];
  } else {
    refs = [{ kind: SecureSettingKind.R2AccessKeyId, profileId }, { kind: SecureSettingKind.R2SecretAccessKey, profileId }];
  }
  try {
    const responses = await Promise.all(refs.map((setting) => bridge.request({ operation: "secure.read", setting })));
    return responses.some((response) => response.kind !== "secure-value" || response.value === null);
  } catch (reason) {
    if (reason instanceof NativeBridgeError) return true;
    throw reason;
  }
}
