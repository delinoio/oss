import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LogtoProvider, type LogtoConfig } from "@logto/react";
import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import { PublicTransportProvider } from "./api/ApiContext";
import { createPublicTransport } from "./api/transports";
import {
  LogtoAuthBridge,
  UnavailableAuthBridge,
} from "./auth/AuthSession";
import { VolatileLogtoClient } from "./auth/VolatileLogtoClient";
import { canonicalAudience, runtimeConfig } from "./config";
import {
  AuthCallbackPage,
  UnavailableCallbackPage,
} from "./pages/AuthCallbackPage";
import { registerServiceWorker } from "./pwa/register";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: false,
    },
    mutations: {
      retry: false,
    },
  },
});

const publicTransport = createPublicTransport({
  baseUrl: runtimeConfig.apiOrigin || window.location.origin,
});

function Providers({
  authConfigured,
  children,
}: {
  authConfigured: boolean;
  children: ReactNode;
}) {
  if (!authConfigured) {
    return <UnavailableAuthBridge>{children}</UnavailableAuthBridge>;
  }
  const logtoConfig: LogtoConfig = {
    appId: runtimeConfig.logto.appId,
    endpoint: runtimeConfig.logto.endpoint,
    resources: [canonicalAudience],
    scopes: [
      "delibase:account:read",
      "delibase:account:write",
      "delibase:organizations:read",
      "delibase:organizations:write",
      "delibase:teams:read",
      "delibase:teams:write",
      "delibase:billing:read",
      "delibase:billing:write",
    ],
  };
  return (
    <LogtoProvider
      config={logtoConfig}
      LogtoClientClass={VolatileLogtoClient}
    >
      <LogtoAuthBridge>{children}</LogtoAuthBridge>
    </LogtoProvider>
  );
}

const authConfigured = runtimeConfig.issues.length === 0;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <PublicTransportProvider transport={publicTransport}>
          <Providers authConfigured={authConfigured}>
            <App
              callbackPage={
                authConfigured ? (
                  <AuthCallbackPage />
                ) : (
                  <UnavailableCallbackPage />
                )
              }
            />
          </Providers>
        </PublicTransportProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);

void registerServiceWorker();
