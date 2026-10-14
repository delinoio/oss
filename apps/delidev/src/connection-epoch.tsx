// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, type ReactNode } from "react";

/**
 * Counter incremented on every verified connection attempt of the same saved
 * identity. The transport and query client can be retained across such a
 * reconnect while the peer behind them is replaced or downgraded, so state
 * derived from peer negotiation (for example advertised capabilities) must be
 * keyed by this epoch as well as by the transport.
 */
const ConnectionEpoch = createContext(0);

export function ConnectionEpochProvider({
  epoch,
  children,
}: {
  epoch: number;
  children: ReactNode;
}) {
  return (
    <ConnectionEpoch.Provider value={epoch}>
      {children}
    </ConnectionEpoch.Provider>
  );
}

export const useConnectionEpoch = () => useContext(ConnectionEpoch);
