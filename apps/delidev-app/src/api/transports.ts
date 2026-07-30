import {
  createClient,
  type Client,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { RealQATrackerService } from "@delinoio/devhud-realqa-connect";

import { canonicalAudience } from "../config";

export type AccessTokenGetter = (audience: string) => Promise<string | undefined>;
export type RealQATrackerClient = Client<typeof RealQATrackerService>;

interface TransportOptions {
  baseUrl: string;
  fetch?: typeof globalThis.fetch;
}

export function createPublicTransport({
  baseUrl,
  configurationValid = true,
  fetch,
}: TransportOptions & { configurationValid?: boolean }): Transport {
  const configured =
    configurationValid && baseUrl === canonicalAudience;
  return createConnectTransport({
    baseUrl: configured ? baseUrl : canonicalAudience,
    fetch: configured
      ? fetch
      : async () => {
          throw new Error(
            `Public catalog requests require valid public configuration and PUBLIC_DELIBASE_API_ORIGIN=${canonicalAudience}.`,
          );
        },
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

export function createRealQATrackerClient({
  audience,
  baseUrl,
  delibaseAudience,
  fetch,
  getAccessToken,
}: TransportOptions & {
  audience: string;
  delibaseAudience: string;
  getAccessToken: AccessTokenGetter;
}): RealQATrackerClient {
  const authorizationInterceptor: Interceptor = (next) => async (request) => {
    const [realqaToken, delibaseToken] = await Promise.all([
      getAccessToken(audience),
      getAccessToken(delibaseAudience),
    ]);
    if (!realqaToken || !delibaseToken) {
      throw new Error(
        "RealQA and DeliDev access tokens are required for this request.",
      );
    }
    request.header.set("Authorization", `Bearer ${realqaToken}`);
    request.header.set(
      "x-delibase-forwarded-user-token",
      delibaseToken,
    );
    request.header.set("Cache-Control", "no-store");
    return next(request);
  };

  return createClient(
    RealQATrackerService,
    createConnectTransport({
      baseUrl,
      fetch,
      interceptors: [authorizationInterceptor],
      useBinaryFormat: false,
    }),
  );
}
