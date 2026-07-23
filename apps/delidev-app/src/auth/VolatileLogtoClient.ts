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
  }

  private get sessionKey() {
    return `logto:${this.appId}:${PersistKey.SignInSession}`;
  }

  async getItem(key: LogtoStorageKey): Promise<string | null> {
    if (key === PersistKey.SignInSession) {
      return sessionStorage.getItem(this.sessionKey);
    }
    return authStateFor(this.appId).get(key) ?? null;
  }

  async setItem(key: LogtoStorageKey, value: string): Promise<void> {
    if (key === PersistKey.SignInSession) {
      sessionStorage.setItem(this.sessionKey, value);
      return;
    }
    authStateFor(this.appId).set(key, value);
  }

  async removeItem(key: LogtoStorageKey): Promise<void> {
    if (key === PersistKey.SignInSession) {
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
