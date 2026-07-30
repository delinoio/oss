import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { create, toBinary } from "@bufbuild/protobuf";
import {
  ErrorDetailSchema,
  ErrorReason,
} from "@delinoio/devhud-realqa-connect";
import axe from "axe-core";
import { describe, expect, it, vi } from "vitest";

import { createRealQAAuthenticatedTransport } from "../api/transports";
import { RealQAGitHubDestinations } from "../components/RealQAGitHubDestinations";
import type { RealQAConfig } from "../config";

const config: RealQAConfig = {
  apiOrigin: "https://realqa.deli.dev",
  audience: "https://realqa.deli.dev",
  githubAppClientId: "fixture-realqa-client",
  githubAppSlug: "fixture-realqa",
  githubCallbackUri: "https://realqa.deli.dev/github/oauth/callback",
  issues: [],
};

function connectJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    headers: { "content-type": "application/json" },
    status,
  });
}

function realQAErrorResponse(
  reason: ErrorReason,
  currentRevision: bigint,
): Response {
  const detail = create(ErrorDetailSchema, {
    currentRevision: {
      etag: `"realqa-r${currentRevision}"`,
      value: currentRevision,
    },
    reason,
  });
  return connectJsonResponse(
    {
      code: "aborted",
      details: [
        {
          type: "devhud.realqa.v1.ErrorDetail",
          value: Buffer.from(
            toBinary(ErrorDetailSchema, detail),
          ).toString("base64"),
        },
      ],
      message: "operation aborted",
    },
    409,
  );
}

function requestBody(
  request: Parameters<typeof fetch>[0],
  init?: Parameters<typeof fetch>[1],
): Promise<Record<string, any>> {
  return new Response(
    init?.body ?? (request instanceof Request ? request.clone().body : null),
  ).json();
}

function renderDestination({
  authorizationNavigator,
  fetchMock,
  owner = {
    canManage: true,
    kind: "organization" as const,
    organizationId: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608",
    organizationName: "Acme",
  },
}: {
  authorizationNavigator?: (target: string) => void;
  fetchMock: typeof fetch;
  owner?:
    | {
        accountId: string;
        kind: "personal";
      }
    | {
        canManage: boolean;
        kind: "organization";
        organizationId: string;
        organizationName: string;
      };
}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  const transport = createRealQAAuthenticatedTransport({
    audience: config.audience,
    baseUrl: config.apiOrigin,
    delibaseAudience: "https://delibase.deli.dev",
    fetch: fetchMock,
    getAccessToken: async (audience) =>
      audience === config.audience ? "realqa-token" : "delibase-token",
  });
  const view = render(
    <QueryClientProvider client={queryClient}>
      <RealQAGitHubDestinations
        authorizationNavigator={authorizationNavigator}
        config={config}
        owner={owner}
        transport={transport}
      />
    </QueryClientProvider>,
  );
  return { ...view, queryClient };
}

function connectedResponse(
  state = "GIT_HUB_CONNECTION_STATE_CONNECTED",
  revision = "7",
) {
  return {
    connection: {
      connectedAt: "2026-07-30T12:00:00Z",
      connectionId: {
        value: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51610",
      },
      githubLogin: "octocat",
      revision: { etag: `connection-${revision}`, value: revision },
      state,
    },
  };
}

function installationResponse() {
  return {
    installations: [
      {
        accountLogin: "acme-github",
        installationId: {
          value: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51611",
        },
        providerInstallationId: "9123",
        revision: { etag: "installation-3", value: "3" },
      },
    ],
    page: {},
  };
}

describe("RealQA GitHub destinations", () => {
  it("requires an organization installation before a Member can authorize GitHub", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(
          connectedResponse("GIT_HUB_CONNECTION_STATE_DISCONNECTED"),
        );
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    renderDestination({
      fetchMock,
      owner: {
        canManage: false,
        kind: "organization",
        organizationId: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608",
        organizationName: "Acme",
      },
    });

    expect(
      await screen.findByRole("button", { name: "Authorize GitHub access" }),
    ).toBeDisabled();
    expect(
      screen.getByText(/Ask an Owner or Admin to connect the installation/),
    ).toBeVisible();
  });

  it("keeps member controls role-bounded and renders only server-filtered repository permissions and definitions", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(connectedResponse());
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(installationResponse());
      }
      if (url.endsWith("/ListRepositories")) {
        return connectJsonResponse({
          page: {},
          repositories: [
            {
              callerCanSubmit: true,
              installationId: {
                value: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51611",
              },
              issuesEnabled: true,
              repository: {
                name: "public-app",
                owner: "acme",
                repositoryId: "101",
              },
            },
            {
              callerCanSubmit: false,
              installationId: {
                value: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51611",
              },
              issuesEnabled: true,
              repository: {
                name: "read-only",
                owner: "acme",
                repositoryId: "102",
              },
            },
          ],
        });
      }
      if (url.endsWith("/GetRepositoryIssueSchema")) {
        return connectJsonResponse({
          schema: {
            issueForms: [
              {
                definition: {
                  definitionId: "form-bug",
                  kind: "REPOSITORY_ISSUE_DEFINITION_KIND_ISSUE_FORM",
                  name: "Bug report",
                  path: ".github/ISSUE_TEMPLATE/bug.yml",
                },
                fields: [
                  {
                    defaultValue: "Reproduction steps",
                    fieldId: "steps",
                    kind: "ISSUE_FORM_FIELD_KIND_TEXTAREA",
                    label: "Steps",
                    renderLanguage: "shell",
                    required: true,
                  },
                  {
                    fieldId: "versions",
                    kind: "ISSUE_FORM_FIELD_KIND_DROPDOWN",
                    label: "Versions",
                    multiple: true,
                    options: [],
                  },
                ],
                issueType: "Bug",
              },
            ],
            markdownTemplates: [
              {
                definition: {
                  definitionId: "template-support",
                  kind:
                    "REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE",
                  name: "Support request",
                  path: ".github/ISSUE_TEMPLATE/support.md",
                },
                issueType: "Task",
              },
            ],
            repository: {
              name: "public-app",
              owner: "acme",
              repositoryId: "101",
            },
            revision: { etag: "schema-9", value: "9" },
          },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderDestination({
      fetchMock,
      owner: {
        canManage: false,
        kind: "organization",
        organizationId: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608",
        organizationName: "Acme",
      },
    });

    expect(
      await screen.findByRole("button", { name: "Authorize GitHub access" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "Disconnect GitHub" }),
    ).not.toBeInTheDocument();
    expect(await screen.findByText("acme/public-app")).toBeVisible();
    expect(screen.getByText("acme/read-only")).toBeVisible();
    expect(
      screen.getByText("No issue submission permission"),
    ).toBeVisible();
    expect(screen.queryByText("acme/private-secret")).not.toBeInTheDocument();
    const reviewButtons = screen.getAllByRole("button", {
      name: "Review definitions",
    });
    expect(reviewButtons[1]).toBeDisabled();
    await user.click(reviewButtons[0]!);

    expect(await screen.findByText("Support request")).toBeVisible();
    expect(screen.getByText("Bug report")).toBeVisible();
    expect(
      screen.getByText(/Steps · Textarea · required · prefilled: Reproduction steps · code language: shell/),
    ).toBeVisible();
    expect(
      screen.getByText(/Versions · Dropdown · multiple selections/),
    ).toBeVisible();
    expect(screen.getByText("Schema revision 9")).toBeVisible();
  });

  it("reuses the UUIDv7 disconnect identity after an ambiguous failure and preserves mappings", async () => {
    const disconnectKeys: string[] = [];
    let disconnected = false;
    let disconnectAttempts = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(
          connectedResponse(
            disconnected
              ? "GIT_HUB_CONNECTION_STATE_DISCONNECTED"
              : "GIT_HUB_CONNECTION_STATE_CONNECTED",
          ),
        );
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(installationResponse());
      }
      if (url.endsWith("/ListRepositories")) {
        return connectJsonResponse({ page: {}, repositories: [] });
      }
      if (url.endsWith("/DisconnectGitHubConnection")) {
        const body = await requestBody(request, init);
        disconnectKeys.push(body.idempotency.value.value);
        expect(body.expectedRevision.value).toBe("7");
        disconnectAttempts += 1;
        if (disconnectAttempts === 1) {
          return connectJsonResponse(
            { code: "unavailable", message: "Response was lost." },
            503,
          );
        }
        disconnected = true;
        return connectJsonResponse({
          connection: connectedResponse(
            "GIT_HUB_CONNECTION_STATE_DISCONNECTED",
          ).connection,
          idempotency: {
            operation: "IDEMPOTENT_OPERATION_DISCONNECT_GITHUB_CONNECTION",
            replayed: true,
          },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderDestination({ fetchMock });

    const trigger = await screen.findByRole("button", {
      name: "Disconnect GitHub",
    });
    await user.click(trigger);
    expect(
      screen.getByRole("dialog", { name: "Disconnect GitHub from RealQA?" }),
    ).toBeVisible();
    expect(
      screen.getByText(/presets and repository mappings stay saved/i),
    ).toBeVisible();
    const dialog = screen.getByRole("dialog", {
      name: "Disconnect GitHub from RealQA?",
    });
    await user.click(
      within(dialog).getByRole("button", { name: "Disconnect GitHub" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "RealQA is temporarily unavailable",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Disconnect GitHub" }),
    );

    const confirmation = await screen.findByText(
      /Existing RealQA presets and destination mappings were preserved/,
    );
    expect(confirmation).toBeVisible();
    expect(disconnectKeys).toHaveLength(2);
    expect(disconnectKeys[1]).toBe(disconnectKeys[0]);
    expect(disconnectKeys[0]).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
    await waitFor(() =>
      expect(document.activeElement).toBe(confirmation),
    );
  });

  it("refreshes a stale disconnect revision and starts a new safe retry", async () => {
    const disconnectKeys: string[] = [];
    const disconnectRevisions: string[] = [];
    let currentRevision = "7";
    let disconnectAttempts = 0;
    const fetchMock = vi.fn<typeof fetch>(async (request, init) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(
          connectedResponse(
            "GIT_HUB_CONNECTION_STATE_CONNECTED",
            currentRevision,
          ),
        );
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(installationResponse());
      }
      if (url.endsWith("/ListRepositories")) {
        return connectJsonResponse({ page: {}, repositories: [] });
      }
      if (url.endsWith("/DisconnectGitHubConnection")) {
        const body = await requestBody(request, init);
        disconnectKeys.push(body.idempotency.value.value);
        disconnectRevisions.push(body.expectedRevision.value);
        disconnectAttempts += 1;
        if (disconnectAttempts === 1) {
          currentRevision = "8";
          return realQAErrorResponse(ErrorReason.STALE_REVISION, 8n);
        }
        return connectJsonResponse({
          connection: connectedResponse(
            "GIT_HUB_CONNECTION_STATE_DISCONNECTED",
            "9",
          ).connection,
          idempotency: {
            operation: "IDEMPOTENT_OPERATION_DISCONNECT_GITHUB_CONNECTION",
            replayed: false,
          },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderDestination({ fetchMock });

    await user.click(
      await screen.findByRole("button", { name: "Disconnect GitHub" }),
    );
    const dialog = screen.getByRole("dialog", {
      name: "Disconnect GitHub from RealQA?",
    });
    const confirm = within(dialog).getByRole("button", {
      name: "Disconnect GitHub",
    });
    await user.click(confirm);
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The GitHub connection changed",
    );
    await waitFor(() => {
      expect(
        fetchMock.mock.calls.filter(([request]) =>
          String(request).endsWith("/GetGitHubConnection"),
        ),
      ).toHaveLength(2);
    });
    await user.click(confirm);

    await waitFor(() => expect(disconnectRevisions).toEqual(["7", "8"]));
    expect(disconnectKeys).toHaveLength(2);
    expect(disconnectKeys[1]).not.toBe(disconnectKeys[0]);
  });

  it("hides a loaded schema when refreshed repository permissions revoke submission", async () => {
    let accessLost = false;
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(connectedResponse());
      }
      if (url.endsWith("/ListGitHubInstallations")) {
        return connectJsonResponse(installationResponse());
      }
      if (url.endsWith("/ListRepositories")) {
        return connectJsonResponse({
          page: {},
          repositories: [
            {
              callerCanSubmit: !accessLost,
              installationId: {
                value: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51611",
              },
              issuesEnabled: true,
              repository: {
                name: "public-app",
                owner: "acme",
                repositoryId: "101",
              },
            },
          ],
        });
      }
      if (url.endsWith("/GetRepositoryIssueSchema")) {
        return connectJsonResponse({
          schema: {
            issueForms: [],
            markdownTemplates: [
              {
                definition: {
                  definitionId: "template-support",
                  name: "Support request",
                  path: ".github/ISSUE_TEMPLATE/support.md",
                },
              },
            ],
            repository: {
              name: "public-app",
              owner: "acme",
              repositoryId: "101",
            },
            revision: { etag: "schema-9", value: "9" },
          },
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    const { queryClient } = renderDestination({ fetchMock });

    await user.click(
      await screen.findByRole("button", { name: "Review definitions" }),
    );
    expect(await screen.findByText("Support request")).toBeVisible();

    accessLost = true;
    await queryClient.refetchQueries({ type: "active" });

    await waitFor(() =>
      expect(
        screen.getByText("No issue submission permission"),
      ).toBeVisible(),
    );
    expect(screen.queryByText("Support request")).not.toBeInTheDocument();
  });

  it("consumes a validated GitHub.com target without retaining it in mutation data", async () => {
    const authorizationNavigator = vi.fn();
    const fetchMock = vi.fn<typeof fetch>(async (request) => {
      const url = String(request);
      if (url.endsWith("/GetGitHubConnection")) {
        return connectJsonResponse(
          connectedResponse("GIT_HUB_CONNECTION_STATE_DISCONNECTED"),
        );
      }
      if (url.endsWith("/StartGitHubConnection")) {
        return connectJsonResponse({
          authorizationTarget:
            "https://github.com/apps/fixture-realqa/installations/new?state=abcdefghijklmnopqrstuvwxyz123456",
          expiresAt: "2026-07-30T12:10:00Z",
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    const { queryClient } = renderDestination({
      authorizationNavigator,
      fetchMock,
      owner: {
        accountId: "018f3f5e-7b01-7a2d-8c3a-4ba8d8b51609",
        kind: "personal",
      },
    });
    await user.click(
      await screen.findByRole("button", { name: "Connect GitHub" }),
    );

    await waitFor(() =>
      expect(authorizationNavigator).toHaveBeenCalledWith(
        "https://github.com/apps/fixture-realqa/installations/new?state=abcdefghijklmnopqrstuvwxyz123456",
      ),
    );
    await waitFor(() =>
      expect(
        queryClient
          .getMutationCache()
          .getAll()
          .every((mutation) => mutation.state.data === undefined),
      ).toBe(true),
    );
  });

  it("disables connection mutations offline and remains accessible", async () => {
    const originalOnline = navigator.onLine;
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    const fetchMock = vi.fn<typeof fetch>(async () =>
      connectJsonResponse(
        connectedResponse("GIT_HUB_CONNECTION_STATE_DISCONNECTED"),
      ),
    );
    const { container } = renderDestination({ fetchMock });

    expect(
      await screen.findByRole("button", { name: "Connect GitHub" }),
    ).toBeDisabled();
    expect(screen.getByText("Reconnect to use this action.")).toBeVisible();
    const result = await axe.run(container, {
      runOnly: {
        type: "tag",
        values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"],
      },
    });
    expect(result.violations).toEqual([]);

    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: originalOnline,
    });
  });
});
