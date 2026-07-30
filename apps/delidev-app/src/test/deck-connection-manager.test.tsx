import axe from "axe-core";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  createDeckIntegrationTransport,
} from "../api/transports";
import {
  AuthSessionProvider,
  AuthStatus,
} from "../auth/AuthSession";
import {
  DeckConnectionManager,
  type DeckConnectionOwner,
} from "../components/DeckConnectionManager";
import {
  canonicalDeckAudience,
  canonicalDeckGitHubCallbackUri,
  runtimeConfig,
} from "../config";

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

function personalOwner() {
  return {
    scope: "OWNER_SCOPE_PERSONAL",
    accountId: { value: "account-id" },
  };
}

function connectedResponse(state = "CONNECTION_STATE_CONNECTED") {
  return {
    connection: {
      connectionId: { value: "01900000-0000-7000-8000-000000000001" },
      githubInstallationId: "42",
      owner: personalOwner(),
      revision: { etag: "opaque-etag", value: "7" },
      state,
    },
  };
}

function installationsResponse() {
  return {
    installations: [
      {
        account: {
          githubAccountId: "99",
          kind: "GIT_HUB_ACCOUNT_KIND_USER",
          login: "octocat",
        },
        githubInstallationId: "42",
        owner: personalOwner(),
        state: "CONNECTION_STATE_CONNECTED",
      },
    ],
    page: {},
  };
}

function renderManager(
  fetchMock: typeof fetch,
  ownerScope: DeckConnectionOwner = {
    accountId: "account-id",
    kind: "personal",
    returnPath: "/account",
  },
) {
  const deckTransport = createDeckIntegrationTransport({
    baseUrl: canonicalDeckAudience,
    fetch: fetchMock,
    getAccessToken: async (audience) =>
      audience === canonicalDeckAudience
        ? "deck-token"
        : "delibase-token",
  });
  return render(
    <AuthSessionProvider
      value={{
        deckTransport,
        signIn: async () => undefined,
        signOut: async () => undefined,
        status: AuthStatus.SignedIn,
      }}
    >
      <DeckConnectionManager
        ownerScope={ownerScope}
      />
    </AuthSessionProvider>,
  );
}

describe("Deck connection manager", () => {
  beforeEach(() => {
    Object.assign(runtimeConfig.deck, {
      apiOrigin: canonicalDeckAudience,
      audience: canonicalDeckAudience,
      githubAppClientId: "Iv1.fixture-client",
      githubAppSlug: "deli-dev-deck",
      githubCallbackUri: canonicalDeckGitHubCallbackUri,
      issues: [],
    });
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: true,
    });
  });

  afterEach(() => {
    Object.assign(runtimeConfig.deck, {
      apiOrigin: "",
      audience: "",
      githubAppClientId: "",
      githubAppSlug: "",
      githubCallbackUri: "",
      issues: ["not configured"],
    });
  });

  it("shows one owner-bound installation, permission state, and revision accessibly", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(
          connectedResponse(
            "CONNECTION_STATE_REAUTHENTICATION_REQUIRED",
          ),
        );
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(installationsResponse());
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const { container } = renderManager(fetchMock);

    expect(
      await screen.findAllByText("Permission review required"),
    ).toHaveLength(2);
    expect(screen.getByText("@octocat")).toBeVisible();
    expect(screen.getByText("Personal GitHub account")).toBeVisible();
    expect(screen.getByText("Connection revision 7")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Review GitHub permissions" }),
    ).toBeEnabled();
    expect(screen.queryByText("42")).not.toBeInTheDocument();
    expect(
      screen.queryByText(/repository|pull request|query|result count/i),
    ).not.toBeInTheDocument();

    const results = await axe.run(container, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
      },
    });
    expect(results.violations).toEqual([]);
  });

  it("binds organization requests to the selected organization owner only", async () => {
    const requestBodies: Array<Record<string, unknown>> = [];
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      requestBodies.push(
        (await new Response(
          init?.body ??
            (request instanceof Request ? request.clone().body : null),
        ).json()) as Record<string, unknown>,
      );
      return String(request).endsWith("/GetGitHubConnection")
        ? connectJsonResponse(
            { code: "not_found", message: "not connected" },
            404,
          )
        : connectJsonResponse({ installations: [], page: {} });
    });
    renderManager(fetchMock, {
      kind: "organization",
      organizationId: "organization-id",
      organizationName: "Acme",
      returnPath: "/o/acme/settings",
    });

    expect(await screen.findAllByText("Disconnected")).toHaveLength(2);
    expect(requestBodies).toHaveLength(2);
    for (const body of requestBodies) {
      expect(body).toMatchObject({
        owner: {
          organizationId: { value: "organization-id" },
          scope: "OWNER_SCOPE_ORGANIZATION",
        },
      });
      expect(JSON.stringify(body)).not.toContain("accountId");
    }
  });

  it("preserves disconnect revision input across an ambiguous retry and restores dialog focus", async () => {
    const disconnectBodies: unknown[] = [];
    let disconnectAttempt = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(connectedResponse());
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(installationsResponse());
      }
      if (url.endsWith("/DisconnectGitHubConnection")) {
        disconnectBodies.push(
          await new Response(
            init?.body ??
              (request instanceof Request
                ? request.clone().body
                : null),
          ).json(),
        );
        disconnectAttempt += 1;
        return disconnectAttempt === 1
          ? connectJsonResponse(
              { code: "unavailable", message: "response lost" },
              503,
            )
          : connectJsonResponse(
              connectedResponse("CONNECTION_STATE_DISCONNECTED"),
            );
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderManager(fetchMock);

    const trigger = await screen.findByRole("button", {
      name: "Disconnect",
    });
    await user.click(trigger);
    expect(
      screen.getByRole("button", { name: "Keep connection" }),
    ).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(trigger).toHaveFocus();

    await user.click(trigger);
    await user.click(
      screen.getByRole("button", { name: "Disconnect GitHub" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "temporarily unavailable",
    );
    await user.click(
      screen.getByRole("button", { name: "Disconnect GitHub" }),
    );

    expect(
      await screen.findByText(/Deck credentials and cached connection data/),
    ).toBeVisible();
    expect(disconnectBodies).toHaveLength(2);
    expect(disconnectBodies[1]).toEqual(disconnectBodies[0]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("replaces a pending disconnect input after the owner changes", async () => {
    const disconnectBodies: unknown[] = [];
    const organizationOwner = {
      organizationId: { value: "organization-id" },
      scope: "OWNER_SCOPE_ORGANIZATION",
    };
    const organizationConnection = {
      connection: {
        connectionId: {
          value: "01900000-0000-7000-8000-000000000002",
        },
        githubInstallationId: "84",
        owner: organizationOwner,
        revision: { etag: "organization-etag", value: "9" },
        state: "CONNECTION_STATE_CONNECTED",
      },
    };
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/DisconnectGitHubConnection")) {
        disconnectBodies.push(
          await new Response(
            init?.body ??
              (request instanceof Request
                ? request.clone().body
                : null),
          ).json(),
        );
        return disconnectBodies.length === 1
          ? connectJsonResponse(
              { code: "unavailable", message: "response lost" },
              503,
            )
          : connectJsonResponse({
              connection: {
                ...organizationConnection.connection,
                state: "CONNECTION_STATE_DISCONNECTED",
              },
            });
      }
      const body = (await new Response(
        init?.body ??
          (request instanceof Request
            ? request.clone().body
            : null),
      ).json()) as {
        owner?: { organizationId?: { value?: string } };
      };
      const organizationRequest =
        body.owner?.organizationId?.value === "organization-id";
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(
          organizationRequest
            ? organizationConnection
            : connectedResponse(),
        );
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(
          organizationRequest
            ? {
                installations: [
                  {
                    account: {
                      githubAccountId: "199",
                      kind: "GIT_HUB_ACCOUNT_KIND_ORGANIZATION",
                      login: "acme",
                    },
                    githubInstallationId: "84",
                    owner: organizationOwner,
                    state: "CONNECTION_STATE_CONNECTED",
                  },
                ],
                page: {},
              }
            : installationsResponse(),
        );
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const deckTransport = createDeckIntegrationTransport({
      baseUrl: canonicalDeckAudience,
      fetch: fetchMock,
      getAccessToken: async (audience) =>
        audience === canonicalDeckAudience
          ? "deck-token"
          : "delibase-token",
    });

    function Parent({
      ownerScope,
    }: {
      ownerScope: DeckConnectionOwner;
    }) {
      return (
        <AuthSessionProvider
          value={{
            deckTransport,
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
          }}
        >
          <DeckConnectionManager ownerScope={ownerScope} />
        </AuthSessionProvider>
      );
    }

    const user = userEvent.setup();
    const { rerender } = render(
      <Parent
        ownerScope={{
          accountId: "account-id",
          kind: "personal",
          returnPath: "/account",
        }}
      />,
    );

    await user.click(
      await screen.findByRole("button", { name: "Disconnect" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Disconnect GitHub" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "temporarily unavailable",
    );

    rerender(
      <Parent
        ownerScope={{
          kind: "organization",
          organizationId: "organization-id",
          organizationName: "Acme",
          returnPath: "/o/acme/settings",
        }}
      />,
    );
    expect(await screen.findByText("@acme")).toBeVisible();
    await user.click(
      screen.getByRole("button", { name: "Disconnect GitHub" }),
    );

    expect(disconnectBodies).toEqual([
      {
        connectionId: {
          value: "01900000-0000-7000-8000-000000000001",
        },
        expectedRevision: { etag: "opaque-etag", value: "7" },
      },
      {
        connectionId: {
          value: "01900000-0000-7000-8000-000000000002",
        },
        expectedRevision: {
          etag: "organization-etag",
          value: "9",
        },
      },
    ]);
  });

  it("disables every mutation after the page goes offline", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) =>
      String(request).endsWith("/GetGitHubConnection")
        ? connectJsonResponse(connectedResponse())
        : connectJsonResponse(installationsResponse()),
    );
    renderManager(fetchMock);

    await screen.findByText("@octocat");
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    window.dispatchEvent(new Event("offline"));

    await waitFor(() => {
      expect(
        screen.getByRole("button", {
          name: "Change GitHub installation",
        }),
      ).toBeDisabled();
      expect(
        screen.getByRole("button", { name: "Disconnect" }),
      ).toBeDisabled();
    });
    expect(screen.getByText("Reconnect to use this action.")).toBeVisible();
  });

  it("renders an explicit offline state without loading connection data", () => {
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    const fetchMock = vi.fn<typeof fetch>();

    renderManager(fetchMock);

    expect(
      screen.getByText(/GitHub connection details are unavailable offline/),
    ).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("replaces an interrupted loading state with offline guidance", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => {
      await new Promise((resolve) => setTimeout(resolve, 100));
      return connectJsonResponse({});
    });

    renderManager(fetchMock);

    expect(
      await screen.findByText(/Loading GitHub connection/),
    ).toBeVisible();

    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    window.dispatchEvent(new Event("offline"));

    expect(
      await screen.findByText(
        /GitHub connection details are unavailable offline/,
      ),
    ).toBeVisible();
    expect(
      screen.queryByText(/Loading GitHub connection/),
    ).not.toBeInTheDocument();
  });

  it("does not expose cached connection data for a different owner", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) =>
      String(request).endsWith("/GetGitHubConnection")
        ? connectJsonResponse(connectedResponse())
        : connectJsonResponse(installationsResponse()),
    );
    const deckTransport = createDeckIntegrationTransport({
      baseUrl: canonicalDeckAudience,
      fetch: fetchMock,
      getAccessToken: async (audience) =>
        audience === canonicalDeckAudience
          ? "deck-token"
          : "delibase-token",
    });

    function Parent() {
      const [ownerScope, setOwnerScope] =
        useState<DeckConnectionOwner>({
          accountId: "account-id",
          kind: "personal",
          returnPath: "/account",
        });
      return (
        <AuthSessionProvider
          value={{
            deckTransport,
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
          }}
        >
          <button
            onClick={() =>
              setOwnerScope({
                kind: "organization",
                organizationId: "organization-id",
                organizationName: "Acme",
                returnPath: "/o/acme/settings",
              })
            }
            type="button"
          >
            Switch owner
          </button>
          <DeckConnectionManager ownerScope={ownerScope} />
        </AuthSessionProvider>
      );
    }

    const user = userEvent.setup();
    render(<Parent />);

    expect(await screen.findByText("@octocat")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Disconnect" }),
    ).toBeEnabled();

    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    window.dispatchEvent(new Event("offline"));
    await user.click(
      screen.getByRole("button", { name: "Switch owner" }),
    );

    expect(
      screen.getByRole("heading", {
        name: "Acme GitHub connection",
      }),
    ).toBeVisible();
    expect(screen.queryByText("@octocat")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Connection revision 7"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Disconnect" }),
    ).not.toBeInTheDocument();
  });

  it("does not reload when an equivalent owner prop is recreated", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) =>
      String(request).endsWith("/GetGitHubConnection")
        ? connectJsonResponse(
            { code: "not_found", message: "not connected" },
            404,
          )
        : connectJsonResponse({ installations: [], page: {} }),
    );
    const deckTransport = createDeckIntegrationTransport({
      baseUrl: canonicalDeckAudience,
      fetch: fetchMock,
      getAccessToken: async (audience) =>
        audience === canonicalDeckAudience
          ? "deck-token"
          : "delibase-token",
    });

    function Parent() {
      const [name, setName] = useState("");
      return (
        <AuthSessionProvider
          value={{
            deckTransport,
            signIn: async () => undefined,
            signOut: async () => undefined,
            status: AuthStatus.SignedIn,
          }}
        >
          <label>
            Organization name
            <input
              onChange={(event) => setName(event.currentTarget.value)}
              value={name}
            />
          </label>
          <DeckConnectionManager
            ownerScope={{
              kind: "organization",
              organizationId: "organization-id",
              organizationName: "Acme",
              returnPath: "/o/acme/settings",
            }}
          />
        </AuthSessionProvider>
      );
    }

    const user = userEvent.setup();
    render(<Parent />);

    await screen.findAllByText("Disconnected");
    expect(fetchMock).toHaveBeenCalledTimes(2);

    await user.type(screen.getByRole("textbox"), "Updated");

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
