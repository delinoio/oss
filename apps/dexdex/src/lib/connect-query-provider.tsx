import type { Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode, useMemo } from "react";
import { dexdexQueryClient } from "./query-client";

export type ConnectQueryProviderProps = {
  children: ReactNode;
  endpointUrl?: string;
};

export function createDexDexTransport(endpointUrl: string): Transport {
  return createConnectTransport({
    baseUrl: endpointUrl,
    useBinaryFormat: true,
  });
}

export function ConnectQueryProvider({
  children,
  endpointUrl = "http://127.0.0.1:7878",
}: ConnectQueryProviderProps) {
  const transport = useMemo(
    () => createDexDexTransport(endpointUrl),
    [endpointUrl],
  );

  return (
    <QueryClientProvider client={dexdexQueryClient}>
      <TransportProvider transport={transport}>{children}</TransportProvider>
    </QueryClientProvider>
  );
}
