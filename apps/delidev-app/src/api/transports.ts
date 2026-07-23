import type { Interceptor, Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

export type AccessTokenGetter = (audience: string) => Promise<string | undefined>;

interface TransportOptions {
  baseUrl: string;
  fetch?: typeof globalThis.fetch;
}

export function createPublicTransport({
  baseUrl,
  fetch,
}: TransportOptions): Transport {
  return createConnectTransport({
    baseUrl,
    fetch,
    useBinaryFormat: false,
  });
}

export function createAuthenticatedTransport({
  audience,
  baseUrl,
  fetch,
  getAccessToken,
}: TransportOptions & {
  audience: string;
  getAccessToken: AccessTokenGetter;
}): Transport {
  const authorizationInterceptor: Interceptor = (next) => async (request) => {
    const token = await getAccessToken(audience);
    if (!token) {
      throw new Error("A Logto access token is required for this request.");
    }
    request.header.set("Authorization", `Bearer ${token}`);
    request.header.set("Cache-Control", "no-store");
    return next(request);
  };

  return createConnectTransport({
    baseUrl,
    fetch,
    interceptors: [authorizationInterceptor],
    useBinaryFormat: false,
  });
}
