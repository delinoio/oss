import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import {
  publishSessionInvalidation,
  subscribeToPersistenceReset,
} from "../runtime/theme";
import {
  AuthFeature,
  safeAuthFailure,
  type AuthFailure,
  type NativeSessionBridge,
  type NativeSessionSnapshot,
} from "./contracts";
import { tauriSessionBridge } from "./tauri";

interface SessionState {
  readonly session: NativeSessionSnapshot;
  readonly failure: AuthFailure | null;
  readonly ready: boolean;
}

interface SessionActions {
  signIn(feature: AuthFeature): Promise<boolean>;
  logout(): Promise<boolean>;
  clearFailure(): void;
}

type SessionContextValue = SessionState & SessionActions;

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({
  bridge = tauriSessionBridge,
  children,
}: {
  readonly bridge?: NativeSessionBridge;
  readonly children: ReactNode;
}) {
  const [session, setSession] = useState<NativeSessionSnapshot>({
    status: "signed-out",
  });
  const [failure, setFailure] = useState<AuthFailure | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let active = true;
    const unsubscribeReset = subscribeToPersistenceReset(() => {
      setFailure(null);
      setSession({ status: "signed-out" });
    });
    void bridge.restore().then(
      (snapshot) => {
        if (!active) return;
        setSession(snapshot);
        setReady(true);
      },
      (error: unknown) => {
        if (!active) return;
        const restoredFailure = safeAuthFailure(error);
        // Authentication initialization cannot prevent the independent local
        // shell from rendering. The failure is retained for a future feature
        // entry point to present.
        setFailure(restoredFailure);
        setSession({
          status:
            restoredFailure.code === "secure-vault-delete-failed"
              ? "cleanup-required"
              : "signed-out",
        });
        setReady(true);
      },
    );
    return () => {
      active = false;
      unsubscribeReset();
    };
  }, [bridge]);

  useEffect(() => {
    if (session.status !== "authenticating") return;
    let active = true;
    let polling = false;
    const poll = async () => {
      if (polling) return;
      polling = true;
      try {
        const snapshot = await bridge.restore();
        if (!active) return;
        setSession(snapshot);
        if (snapshot.status !== "authenticating") {
          setFailure(null);
        }
      } catch (error: unknown) {
        if (!active) return;
        setFailure(safeAuthFailure(error));
        try {
          const snapshot = await bridge.restore();
          if (!active) return;
          setSession(snapshot);
        } catch (restoreError: unknown) {
          if (!active) return;
          const restoredFailure = safeAuthFailure(restoreError);
          setSession({
            status:
              restoredFailure.code === "secure-vault-delete-failed"
                ? "cleanup-required"
                : "signed-out",
          });
        }
      } finally {
        polling = false;
      }
    };
    const timer = window.setInterval(() => void poll(), 500);
    void poll();
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [bridge, session.status]);

  const signIn = useCallback(
    async (feature: AuthFeature) => {
      setFailure(null);
      try {
        const snapshot = await bridge.start(feature);
        setSession(snapshot);
        return true;
      } catch (error: unknown) {
        setFailure(safeAuthFailure(error));
        return false;
      }
    },
    [bridge],
  );
  const logout = useCallback(async () => {
    setFailure(null);
    // Clear the frontend view immediately. Native logout independently clears
    // all memory tokens before attempting its exact secure-vault deletion.
    setSession({ status: "signed-out" });
    publishSessionInvalidation();
    try {
      const snapshot = await bridge.logout();
      setSession(snapshot);
      return true;
    } catch (error: unknown) {
      const nextFailure = safeAuthFailure(error);
      setFailure(nextFailure);
      if (nextFailure.code === "secure-vault-delete-failed") {
        setSession({ status: "cleanup-required" });
      }
      return false;
    } finally {
      // Republish after native logout has cleared accepted sources so a
      // cross-window render racing the first signal is invalidated again.
      publishSessionInvalidation();
    }
  }, [bridge]);
  const clearFailure = useCallback(() => setFailure(null), []);

  const value = useMemo(
    () => ({
      session,
      failure,
      ready,
      signIn,
      logout,
      clearFailure,
    }),
    [clearFailure, failure, logout, ready, session, signIn],
  );

  return <SessionContext value={value}>{children}</SessionContext>;
}

export function useSession(): SessionContextValue {
  const value = use(SessionContext);
  if (value === null) {
    throw new Error("DevHud authentication must be used inside SessionProvider.");
  }
  return value;
}

export { AuthFeature };
