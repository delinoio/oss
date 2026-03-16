"use client";

import { TransportProvider } from "@connectrpc/connect-query";
import { ReactNode, useMemo } from "react";

import { createDevkitTransport } from "@/lib/transport";

export function CommitTrackerTransportProvider({ children }: { children: ReactNode }) {
  const transport = useMemo(() => createDevkitTransport("commit-tracker"), []);
  return (
    <TransportProvider transport={transport}>{children}</TransportProvider>
  );
}
