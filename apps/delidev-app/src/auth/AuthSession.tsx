import { useLogto } from "@logto/react";
import type { Transport } from "@connectrpc/connect";
import {
  createContext,
  use,
  useCallback,
  useMemo,
  type ReactNode,
} from "react";

import { runtimeConfig } from "../config";
import { createAuthenticatedTransport } from "../api/transports";

export enum AuthStatus {
  Loading = "loading",
  SignedOut = "signed-out",
  SignedIn = "signed-in",
  Unavailable = "unavailable",
}

export interface AuthSessionValue {
  status: AuthStatus;
  error?: string;
  transport?: Transport;
  signIn: (returnTo?: string) => Promise<void>;
  signOut: () => Promise<void>;
}

const AuthSessionContext = createContext<AuthSessionValue | undefined>(undefined);

export function useAuthSession(): AuthSessionValue {
  const session = use(AuthSessionContext);
  if (!session) {
    throw new Error("AuthSessionProvider is missing.");
  }
  return session;
}

export function AuthSessionProvider({
  children,
  value,
}: {
  children: ReactNode;
  value: AuthSessionValue;
}) {
  return <AuthSessionContext value={value}>{children}</AuthSessionContext>;
}

export function LogtoAuthBridge({ children }: { children: ReactNode }) {
  const {
    error,
    getAccessToken,
    isAuthenticated,
    isLoading,
    signIn,
    signOut,
  } = useLogto();

  const transport = useMemo(
    () =>
      createAuthenticatedTransport({
        audience: runtimeConfig.logto.audience,
        baseUrl: runtimeConfig.apiOrigin,
        getAccessToken: (audience) => getAccessToken(audience),
      }),
    [getAccessToken],
  );

  const startSignIn = useCallback(
    async (returnTo = window.location.pathname + window.location.search) => {
      sessionStorage.setItem("delidev:return-to", returnTo);
      await signIn(`${runtimeConfig.appOrigin}/auth/callback`);
    },
    [signIn],
  );

  const startSignOut = useCallback(async () => {
    await signOut(`${runtimeConfig.appOrigin}/`);
  }, [signOut]);

  const value = useMemo<AuthSessionValue>(
    () => ({
      error: error?.message,
      signIn: startSignIn,
      signOut: startSignOut,
      status: isLoading
        ? AuthStatus.Loading
        : isAuthenticated
          ? AuthStatus.SignedIn
          : AuthStatus.SignedOut,
      transport: isAuthenticated ? transport : undefined,
    }),
    [
      error,
      isAuthenticated,
      isLoading,
      startSignIn,
      startSignOut,
      transport,
    ],
  );

  return <AuthSessionProvider value={value}>{children}</AuthSessionProvider>;
}

export function UnavailableAuthBridge({ children }: { children: ReactNode }) {
  const value = useMemo<AuthSessionValue>(
    () => ({
      error:
        "Authentication is not configured. Add the browser-safe Logto environment values.",
      signIn: async () => undefined,
      signOut: async () => undefined,
      status: AuthStatus.Unavailable,
    }),
    [],
  );
  return <AuthSessionProvider value={value}>{children}</AuthSessionProvider>;
}
