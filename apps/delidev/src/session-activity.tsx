// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
const Context = createContext(true);
export function SessionActivityProvider({ active, children }: { active: boolean; children: ReactNode }) {
  return <Context.Provider value={active}>{children}</Context.Provider>;
}
export function useSessionActive() { return useContext(Context); }
// Preserve Connect Query's generic public signature. This adapter gates only
// observation admission; retained mutation controllers remain their owners.
export const useSessionQuery: typeof useQuery = ((...args: Parameters<typeof useQuery>) => {
  const active = useSessionActive();
  return useQuery(args[0], args[1], { ...args[2], enabled: active && args[2]?.enabled !== false });
}) as typeof useQuery;
