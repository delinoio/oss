import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import axe from "axe-core";
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { PublicTransportProvider } from "../api/ApiContext";
import { createPublicTransport } from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import { AppFrame } from "../components/AppFrame";
import { HomePage } from "../pages/HomePage";

describe("public landing accessibility", () => {
  it("has no automatically detectable WCAG 2.2 A/AA violations", async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const transport = createPublicTransport({
      baseUrl: "https://delibase.deli.dev",
      fetch: async () =>
        new Response(JSON.stringify({ apps: [] }), {
          headers: { "content-type": "application/json" },
        }),
    });
    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <PublicTransportProvider transport={transport}>
            <AuthSessionProvider
              value={{
                signIn: async () => undefined,
                signOut: async () => undefined,
                status: AuthStatus.SignedOut,
              }}
            >
              <AppFrame>
                <HomePage />
              </AppFrame>
            </AuthSessionProvider>
          </PublicTransportProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    const result = await axe.run(container, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
      },
    });
    expect(result.violations).toEqual([]);
  });
});
