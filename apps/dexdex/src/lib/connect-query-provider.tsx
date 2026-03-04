import type { Interceptor, Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode, useMemo } from "react";
import { dexdexQueryClient } from "./query-client";

export type ConnectQueryProviderProps = {
  children: ReactNode;
  endpointUrl?: string;
  bearerToken?: string;
};

function normalizeBearerToken(bearerToken?: string): string | undefined {
  if (typeof bearerToken !== "string") {
    return undefined;
  }

  const trimmed = bearerToken.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

export function createBearerTokenInterceptor(
  bearerToken?: string,
): Interceptor {
  const normalizedToken = normalizeBearerToken(bearerToken);

  return (next) => async (request) => {
    if (normalizedToken) {
      request.header.set("Authorization", `Bearer ${normalizedToken}`);
    }

    return next(request);
  };
}

export function createDexDexTransport(
  endpointUrl: string,
  bearerToken?: string,
): Transport {
  const normalizedToken = normalizeBearerToken(bearerToken);

  return createConnectTransport({
    baseUrl: endpointUrl,
    useBinaryFormat: true,
    interceptors: normalizedToken
      ? [createBearerTokenInterceptor(normalizedToken)]
      : undefined,
  });
}

export function ConnectQueryProvider({
  children,
  endpointUrl = "http://127.0.0.1:7878",
  bearerToken,
}: ConnectQueryProviderProps) {
  const transport = useMemo(
    () => createDexDexTransport(endpointUrl, bearerToken),
    [bearerToken, endpointUrl],
  );

  return (
    <QueryClientProvider client={dexdexQueryClient}>
      <TransportProvider transport={transport}>{children}</TransportProvider>
    </QueryClientProvider>
  );
}
