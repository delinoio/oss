import type { Transport } from "@connectrpc/connect";
import { createContext, use, type ReactNode } from "react";

const PublicTransportContext = createContext<Transport | undefined>(undefined);

export function PublicTransportProvider({
  children,
  transport,
}: {
  children: ReactNode;
  transport: Transport;
}) {
  return (
    <PublicTransportContext value={transport}>
      {children}
    </PublicTransportContext>
  );
}

export function usePublicTransport(): Transport {
  const transport = use(PublicTransportContext);
  if (!transport) {
    throw new Error("PublicTransportProvider is missing.");
  }
  return transport;
}
