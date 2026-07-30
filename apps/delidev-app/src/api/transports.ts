import type { Interceptor, Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import {
  canonicalAudience,
  canonicalDeckAudience,
} from "../config";

export type AccessTokenGetter = (audience: string) => Promise<string | undefined>;

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

export function createDeckIntegrationTransport({
  baseUrl,
  fetch,
  getAccessToken,
}: TransportOptions & {
  getAccessToken: AccessTokenGetter;
}): Transport {
  const configured = baseUrl === canonicalDeckAudience;
  const authorizationInterceptor: Interceptor = (next) => async (request) => {
    if (
      !configured ||
      request.service.typeName !==
        "devhud.deck.v1.DeckIntegrationService"
    ) {
      throw new Error(
        "Only the configured Deck integration service is available.",
      );
    }
    const [deckToken, delibaseToken] = await Promise.all([
      getAccessToken(canonicalDeckAudience),
      getAccessToken(canonicalAudience),
    ]);
    if (!deckToken || !delibaseToken) {
      throw new Error(
        "Matching Deck and DeliDev access tokens are required for this request.",
      );
    }
    request.header.set("Authorization", `Bearer ${deckToken}`);
    request.header.set(
      "x-devhud-deck-forwarded-delibase-token",
      delibaseToken,
    );
    request.header.set("Cache-Control", "no-store");
    return next(request);
  };

  return createConnectTransport({
    baseUrl: configured ? baseUrl : canonicalDeckAudience,
    fetch: configured
      ? fetch
      : async () => {
          throw new Error(
            `Deck requests require PUBLIC_DECK_API_ORIGIN=${canonicalDeckAudience}.`,
          );
        },
    interceptors: [authorizationInterceptor],
    useBinaryFormat: false,
  });
}
