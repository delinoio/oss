"use client";

import { QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";

import { queryClient } from "@/lib/query-client";

export function DevkitProviders({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}
