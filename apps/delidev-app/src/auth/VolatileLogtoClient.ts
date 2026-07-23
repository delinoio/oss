import {
  BaseClient,
  PersistKey,
  createRequester,
  generateCodeChallenge,
  generateCodeVerifier,
  generateState,
  type ClientAdapter,
  type LogtoConfig,
} from "@logto/browser";

type LogtoStorage = ClientAdapter["storage"];
type LogtoStorageKey = Parameters<LogtoStorage["getItem"]>[0];

const volatileAuthState = new Map<string, Map<LogtoStorageKey, string>>();
const volatileSignInSessions = new Map<string, string>();
let pendingSealedReturnPath: string | undefined;
let callbackSealedReturnPath: string | undefined;

interface SealedSignInSession {
  ciphertext: string;
  iv: string;
  version: 1;
}

export function prepareSealedSignInReturnPath(returnPath: string) {
  pendingSealedReturnPath = returnPath;
  callbackSealedReturnPath = undefined;
}

export function clearPendingSealedSignInReturnPath() {
  pendingSealedReturnPath = undefined;
}

export function consumeSealedSignInReturnPath(): string | undefined {
  const returnPath = callbackSealedReturnPath;
  callbackSealedReturnPath = undefined;
  return returnPath;
}

function callbackState(): string | undefined {
  const state = new URL(window.location.href).searchParams.get("state");
  return state || undefined;
}

function encodeBase64(value: Uint8Array): string {
  return btoa(String.fromCharCode(...value));
}

function decodeBase64(value: string): Uint8Array<ArrayBuffer> {
  const decoded = atob(value);
  const bytes = new Uint8Array(decoded.length);
  for (let index = 0; index < decoded.length; index += 1) {
    bytes[index] = decoded.charCodeAt(index);
  }
  return bytes;
}

async function importSealingKey(
  state: string,
  usages: Array<"decrypt" | "encrypt">,
): Promise<CryptoKey> {
  const keyBytes = await globalThis.crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(state),
  );
  return globalThis.crypto.subtle.importKey(
    "raw",
    keyBytes,
    "AES-GCM",
    false,
    usages,
  );
}

async function sealSignInSession(
  value: string,
  returnPath: string,
): Promise<string> {
  const session = JSON.parse(value) as { state?: unknown };
  if (typeof session.state !== "string" || !session.state) {
    throw new Error("Logto sign-in state is missing.");
  }
  const iv = globalThis.crypto.getRandomValues(new Uint8Array(12));
  const key = await importSealingKey(session.state, ["encrypt"]);
  const plaintext = new TextEncoder().encode(
    JSON.stringify({ ...session, returnPath }),
  );
  const ciphertext = await globalThis.crypto.subtle.encrypt(
    { iv, name: "AES-GCM" },
    key,
    plaintext,
  );
  return JSON.stringify({
    ciphertext: encodeBase64(new Uint8Array(ciphertext)),
    iv: encodeBase64(iv),
    version: 1,
  } satisfies SealedSignInSession);
}

function parseSealedSignInSession(
  value: string,
): SealedSignInSession | undefined {
  try {
    const candidate = JSON.parse(value) as Partial<SealedSignInSession>;
    if (
      candidate.version === 1 &&
      typeof candidate.ciphertext === "string" &&
      typeof candidate.iv === "string"
    ) {
      return candidate as SealedSignInSession;
    }
  } catch {
    // Plain Logto sign-in sessions are expected for non-sensitive returns.
  }
  return undefined;
}

async function openSignInSession(
  sealed: SealedSignInSession,
  state: string,
): Promise<string> {
  const key = await importSealingKey(state, ["decrypt"]);
  const plaintext = await globalThis.crypto.subtle.decrypt(
    { iv: decodeBase64(sealed.iv), name: "AES-GCM" },
    key,
    decodeBase64(sealed.ciphertext),
  );
  return new TextDecoder().decode(plaintext);
}

function authStateFor(appId: string) {
  const existing = volatileAuthState.get(appId);
  if (existing) return existing;
  const state = new Map<LogtoStorageKey, string>();
  volatileAuthState.set(appId, state);
  return state;
}

function clearLegacyPersistentState(appId: string) {
  const prefix = `logto:${appId}`;
  for (let index = localStorage.length - 1; index >= 0; index -= 1) {
    const key = localStorage.key(index);
    if (key === prefix || key?.startsWith(`${prefix}:`)) {
      localStorage.removeItem(key);
    }
  }
}

export class VolatileLogtoStorage implements LogtoStorage {
  readonly appId: string;

  constructor(appId: string) {
    this.appId = appId;
    clearLegacyPersistentState(appId);
    const storedSession = sessionStorage.getItem(this.sessionKey);
    if (
      storedSession &&
      parseSealedSignInSession(storedSession) &&
      !callbackState()
    ) {
      sessionStorage.removeItem(this.sessionKey);
    }
  }

  private get sessionKey() {
    return `logto:${this.appId}:${PersistKey.SignInSession}`;
  }

  async getItem(key: LogtoStorageKey): Promise<string | null> {
    if (key === PersistKey.SignInSession) {
      const volatileSession = volatileSignInSessions.get(this.appId);
      if (volatileSession) return volatileSession;

      const storedSession = sessionStorage.getItem(this.sessionKey);
      if (!storedSession) return null;
      const sealedSession = parseSealedSignInSession(storedSession);
      if (!sealedSession) return storedSession;

      const state = callbackState();
      if (!state) return null;
      try {
        // The invitation URL cannot be persisted in plaintext across a
        // full-page OIDC redirect. Bind it to Logto's high-entropy state and
        // remove the ciphertext as soon as the callback restores it. This can
        // be removed if the server gains an HttpOnly invitation handoff.
        const openedSession = await openSignInSession(sealedSession, state);
        const parsedSession = JSON.parse(openedSession) as {
          returnPath?: unknown;
        };
        if (typeof parsedSession.returnPath === "string") {
          callbackSealedReturnPath = parsedSession.returnPath;
        }
        volatileSignInSessions.set(this.appId, openedSession);
        sessionStorage.removeItem(this.sessionKey);
        return openedSession;
      } catch {
        sessionStorage.removeItem(this.sessionKey);
        return null;
      }
    }
    return authStateFor(this.appId).get(key) ?? null;
  }

  async setItem(key: LogtoStorageKey, value: string): Promise<void> {
    if (key === PersistKey.SignInSession) {
      volatileSignInSessions.delete(this.appId);
      if (pendingSealedReturnPath) {
        const returnPath = pendingSealedReturnPath;
        const sealedSession = await sealSignInSession(value, returnPath);
        pendingSealedReturnPath = undefined;
        sessionStorage.setItem(this.sessionKey, sealedSession);
        return;
      }
      sessionStorage.setItem(this.sessionKey, value);
      return;
    }
    authStateFor(this.appId).set(key, value);
  }

  async removeItem(key: LogtoStorageKey): Promise<void> {
    if (key === PersistKey.SignInSession) {
      volatileSignInSessions.delete(this.appId);
      sessionStorage.removeItem(this.sessionKey);
      return;
    }
    authStateFor(this.appId).delete(key);
  }
}

export class VolatileLogtoClient extends BaseClient {
  constructor(config: LogtoConfig) {
    super(config, {
      generateCodeChallenge,
      generateCodeVerifier,
      generateState,
      navigate: (url) => window.location.assign(url),
      requester: createRequester(fetch),
      storage: new VolatileLogtoStorage(config.appId),
    });
  }
}
