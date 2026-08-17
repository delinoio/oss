import LogtoClient from "@logto/browser";
import type { GetBootstrapResponse } from "@delinoio/devhud-api-client";

const NONCE_KEY = "devhud.admin.oidc_nonce";

function randomNonce(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

export class AdminAuth {
  readonly client: LogtoClient;

  constructor(
    private readonly issuer: string,
    private readonly audience: string,
    private readonly clientId: string,
    readonly redirectUri: string,
  ) {
    this.client = new LogtoClient({
      endpoint: issuer,
      appId: clientId,
      resources: [audience],
      scopes: ["email", "roles"],
    });
  }

  static fromBootstrap(bootstrap: GetBootstrapResponse): AdminAuth {
    const { logtoIssuer, logtoAudience, logtoClients, logtoRedirects } =
      bootstrap;
    if (!logtoIssuer || !logtoAudience || !logtoClients?.admin || !logtoRedirects?.admin) {
      throw new Error("Administrator authentication bootstrap is incomplete.");
    }
    return new AdminAuth(
      logtoIssuer,
      logtoAudience,
      logtoClients.admin,
      logtoRedirects.admin,
    );
  }

  async begin(): Promise<void> {
    const nonce = randomNonce();
    sessionStorage.setItem(NONCE_KEY, nonce);
    // Logto supplies Authorization Code, S256 PKCE, and state validation.
    // We add and verify an OIDC nonce because it is not persisted by the SDK.
    await this.client.signIn({
      redirectUri: this.redirectUri,
      postRedirectUri: new URL("/admin/", window.location.origin),
      extraParams: { nonce },
    });
  }

  async completeCallback(currentUrl: string): Promise<boolean> {
    if (!(await this.client.isSignInRedirected(currentUrl))) return false;
    const expectedNonce = sessionStorage.getItem(NONCE_KEY);
    if (!expectedNonce) throw new Error("The sign-in nonce is missing.");
    await this.client.handleSignInCallback(currentUrl);
    const claims = await this.client.getIdTokenClaims();
    sessionStorage.removeItem(NONCE_KEY);
    if (claims.nonce !== expectedNonce) {
      await this.client.clearAllTokens();
      throw new Error("The sign-in nonce did not match.");
    }
    return true;
  }

  isAuthenticated(): Promise<boolean> {
    return this.client.isAuthenticated();
  }

  accessToken(): Promise<string> {
    return this.client.getAccessToken(this.audience);
  }

  signOut(): Promise<void> {
    return this.client.signOut(new URL("/admin/", window.location.origin).href);
  }
}

export const authStorage = { nonceKey: NONCE_KEY };
