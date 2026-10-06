// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, type ReactNode } from "react";

const BrowserHost = createContext(true);
export function BrowserHostProvider({ available, children }: { available: boolean; children: ReactNode }) {
  return <BrowserHost.Provider value={available}>{children}</BrowserHost.Provider>;
}
export const useBrowserHost = () => useContext(BrowserHost);
