import { ProjectId, type GetBootstrapResponse, type StaticCapability } from "@delinoio/devhud-api-client";
import LogtoClient, { createRequester, isLogtoRequestError, LogtoClientError, type ClientAdapter, type Storage as LogtoStorage } from "@logto/client";
import { isValidLogtoAudience, normalizeLogtoIssuer, normalizePublicAssetUrl } from "./identity-contract.ts";
import { nativeBridge, RuntimePlatform, SecureSettingKind, type NativeBridgeV1, type RuntimePlatform as RuntimePlatformType } from "./native-bridge";

export const NativeAuthCallback = "devhud://auth/callback" as const;
export const SupportedProtocolSchemaVersion = 1 as const;

export interface ValidatedBootstrap {
  readonly issuer: string;
  readonly audience: string;
  readonly clientId: string;
  readonly redirectUri: typeof NativeAuthCallback;
  readonly publicAssetBaseUrl: string | null;
  readonly capabilities: readonly StaticCapability[];
}

export class BootstrapContractError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = "BootstrapContractError";
  }
}

export function validateBootstrap(response: GetBootstrapResponse, platform: RuntimePlatformType): ValidatedBootstrap {
  if (response.projectId !== ProjectId.DEVHUD) throw new BootstrapContractError("Bootstrap project ID does not match DevHud");
  if (response.protocolSchemaVersion !== SupportedProtocolSchemaVersion) throw new BootstrapContractError("unsupported protocol schema version");
  const issuer = normalizeLogtoIssuer(response.logtoIssuer);
  if (issuer === null) throw new BootstrapContractError("Logto issuer must be an HTTPS or loopback HTTP URL without credentials, query, or fragment");
  if (!isValidLogtoAudience(response.logtoAudience)) throw new BootstrapContractError("Logto audience must be nonblank");
  const audience = response.logtoAudience;
  if (response.logtoRedirects?.native !== NativeAuthCallback) throw new BootstrapContractError("native redirect URI does not match the application contract");
  const clientId = platform === RuntimePlatform.Ios ? response.logtoClients?.ios : platform === RuntimePlatform.Android ? response.logtoClients?.android : response.logtoClients?.desktop;
  if (clientId === undefined || !/^[\x21-\x7e]{1,256}$/u.test(clientId)) throw new BootstrapContractError("platform Logto client ID is missing or invalid");
  const publicAssetBaseUrl = normalizePublicAssetUrl(response.publicAssetBaseUrl);
  if (publicAssetBaseUrl === null) throw new BootstrapContractError("public asset base URL must be HTTPS or loopback HTTP without credentials, query, or fragment");
  const capabilities = Object.freeze([...new Set(response.capabilities ?? [])]);
  return { issuer, audience, clientId, redirectUri: NativeAuthCallback, publicAssetBaseUrl, capabilities };
}

export function isTerminalAccessTokenError(reason: unknown): boolean {
  return (reason instanceof LogtoClientError && reason.code === "not_authenticated")
    || (isLogtoRequestError(reason) && reason.code === "invalid_grant");
}

export class SecureLogtoStorage implements LogtoStorage<string> {
  readonly #bridge: NativeBridgeV1;
  readonly #profileId: string;
  #pending: Promise<void> = Promise.resolve();

  constructor(bridge: NativeBridgeV1, profileId: string) {
    this.#bridge = bridge;
    this.#profileId = profileId;
  }

  async getItem(key: string): Promise<string | null> {
    await this.#pending;
    const values = await this.#read();
    return values[key] ?? null;
  }

  async setItem(key: string, value: string): Promise<void> {
    await this.#mutate((values) => ({ ...values, [key]: value }));
  }

  async removeItem(key: string): Promise<void> {
    await this.#mutate((values) => {
      const next = { ...values };
      delete next[key];
      return next;
    });
  }

  async clear(): Promise<void> {
    await this.#enqueue(async () => {
      await this.#bridge.request({ operation: "secure.remove", setting: this.#setting() });
    });
  }

  async #mutate(update: (values: Record<string, string>) => Record<string, string>): Promise<void> {
    await this.#enqueue(async () => {
      const next = update(await this.#read());
      if (Object.keys(next).length === 0) {
        await this.#bridge.request({ operation: "secure.remove", setting: this.#setting() });
      } else {
        await this.#bridge.request({ operation: "secure.write", setting: this.#setting(), value: JSON.stringify(next) });
      }
    });
  }

  async #enqueue(operation: () => Promise<void>): Promise<void> {
    const current = this.#pending.then(operation);
    this.#pending = current.catch(() => {});
    await current;
  }

  async #read(): Promise<Record<string, string>> {
    const response = await this.#bridge.request({ operation: "secure.read", setting: this.#setting() });
    if (response.kind !== "secure-value" || response.value === null) return {};
    try {
      const parsed: unknown = JSON.parse(response.value);
      if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed) || Object.values(parsed).some((value) => typeof value !== "string")) throw new Error();
      return parsed as Record<string, string>;
    } catch {
      throw new TypeError("secure Logto session is malformed");
    }
  }

  #setting() {
    return { kind: SecureSettingKind.LogtoSession, profileId: this.#profileId } as const;
  }
}

export interface IdentitySession {
  readonly client: LogtoClient;
  readonly storage: SecureLogtoStorage;
  readonly getAccessToken: () => Promise<string>;
  readonly isAuthenticated: () => Promise<boolean>;
  readonly signIn: () => Promise<void>;
  readonly handleCallback: (url: string) => Promise<void>;
  readonly clear: () => Promise<void>;
}

export async function createIdentitySession(bootstrap: ValidatedBootstrap, apiOrigin: string, bridge: NativeBridgeV1 = nativeBridge): Promise<IdentitySession> {
  const storage = new SecureLogtoStorage(bridge, await sessionProfileId(apiOrigin));
  const adapter: ClientAdapter = {
    requester: createRequester(fetch),
    storage,
    navigate: async (url, parameters) => {
      if (parameters.for === "post-sign-in") return;
      await bridge.request({ operation: "auth.open-system-browser", url, issuer: bootstrap.issuer });
    },
    generateState: () => randomBase64Url(32),
    generateCodeVerifier: () => randomBase64Url(64),
    generateCodeChallenge: async (verifier) => base64Url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)))),
  };
  const client = new LogtoClient({ endpoint: bootstrap.issuer, appId: bootstrap.clientId, resources: [bootstrap.audience] }, adapter);
  let currentAccessToken: Promise<string> | null = null;
  return {
    client,
    storage,
    getAccessToken: () => {
      if (currentAccessToken === null) {
        const tracked = client.getAccessToken(bootstrap.audience).finally(() => {
          if (currentAccessToken === tracked) currentAccessToken = null;
        });
        currentAccessToken = tracked;
      }
      return currentAccessToken;
    },
    isAuthenticated: () => client.isAuthenticated(),
    signIn: () => client.signIn({ redirectUri: bootstrap.redirectUri }),
    handleCallback: (url) => client.handleSignInCallback(url),
    clear: async () => {
      const accessToken = currentAccessToken;
      if (accessToken !== null) await accessToken.catch(() => {});
      await storage.clear();
    },
  };
}

export async function sessionProfileId(apiOrigin: string): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(new URL(apiOrigin).origin)));
  return `origin.${base64Url(digest)}`;
}

function randomBase64Url(bytes: number): string {
  const value = new Uint8Array(bytes);
  crypto.getRandomValues(value);
  return base64Url(value);
}

function base64Url(value: Uint8Array): string {
  let binary = "";
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/u, "");
}
