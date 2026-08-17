import type { GetBootstrapResponse } from "@delinoio/devhud-api-client";
import LogtoClient, { createRequester, type ClientAdapter, type Storage as LogtoStorage } from "@logto/client";
import { nativeBridge, RuntimePlatform, SecureSettingKind, type NativeBridgeV1, type RuntimePlatform as RuntimePlatformType } from "./native-bridge";

export const NativeAuthCallback = "devhud://auth/callback" as const;
export const SupportedProtocolSchemaVersion = 1 as const;

export interface ValidatedBootstrap {
  readonly issuer: string;
  readonly audience: string;
  readonly clientId: string;
  readonly redirectUri: typeof NativeAuthCallback;
}

export class BootstrapContractError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = "BootstrapContractError";
  }
}

export function validateBootstrap(response: GetBootstrapResponse, platform: RuntimePlatformType): ValidatedBootstrap {
  if (response.protocolSchemaVersion !== SupportedProtocolSchemaVersion) throw new BootstrapContractError("unsupported protocol schema version");
  if (response.apiVersion !== "v1") throw new BootstrapContractError("unsupported API version");
  const issuer = secureOrigin(response.logtoIssuer, "Logto issuer");
  const audience = secureOrigin(response.logtoAudience, "Logto audience");
  if (response.logtoRedirects?.native !== NativeAuthCallback) throw new BootstrapContractError("native redirect URI does not match the application contract");
  const clientId = platform === RuntimePlatform.Ios ? response.logtoClients?.ios : platform === RuntimePlatform.Android ? response.logtoClients?.android : response.logtoClients?.desktop;
  if (clientId === undefined || !/^[\x21-\x7e]{1,256}$/u.test(clientId)) throw new BootstrapContractError("platform Logto client ID is missing or invalid");
  return { issuer, audience, clientId, redirectUri: NativeAuthCallback };
}

function secureOrigin(value: string, label: string): string {
  try {
    const url = new URL(value);
    if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash || url.pathname !== "/") throw new Error();
    return url.origin;
  } catch {
    throw new BootstrapContractError(`${label} must be an HTTPS origin`);
  }
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
    const previous = this.#pending;
    this.#pending = previous.then(async () => {
      await this.#bridge.request({ operation: "secure.remove", setting: this.#setting() });
    });
    await this.#pending;
  }

  async #mutate(update: (values: Record<string, string>) => Record<string, string>): Promise<void> {
    const previous = this.#pending;
    this.#pending = previous.then(async () => {
      const next = update(await this.#read());
      if (Object.keys(next).length === 0) {
        await this.#bridge.request({ operation: "secure.remove", setting: this.#setting() });
      } else {
        await this.#bridge.request({ operation: "secure.write", setting: this.#setting(), value: JSON.stringify(next) });
      }
    });
    await this.#pending;
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
      currentAccessToken ??= client.getAccessToken(bootstrap.audience).catch((reason) => {
        currentAccessToken = null;
        throw reason;
      });
      return currentAccessToken.then((token) => {
        currentAccessToken = null;
        return token;
      });
    },
    isAuthenticated: () => client.isAuthenticated(),
    signIn: () => client.signIn({ redirectUri: bootstrap.redirectUri }),
    handleCallback: (url) => client.handleSignInCallback(url),
    clear: async () => { await storage.clear(); },
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
