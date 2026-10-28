// SPDX-License-Identifier: Apache-2.0
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import { createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  ProviderService,
  PoolService,
  RoutingService,
  ProviderConnectionSchema,
  OrganizationSchema,
  GetSessionResponseSchema,
  ProviderAuthKind,
  ProviderWire,
  Role,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";
const organization = create(OrganizationSchema, {
  id: "org",
  name: "Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, { userId: "person" });
const source = create(ProviderConnectionSchema, {
  id: "connection",
  providerId: "custom",
  name: "Custom endpoint",
  authKind: ProviderAuthKind.API_KEY,
  configuration: {
    wire: ProviderWire.RESPONSES,
    responsesPath: "/generate",
    modelWires: { "exact-model": ProviderWire.CHAT },
    policy: { parallelToolCalls: false },
  },
  customHeaderNames: ["X-Private"],
});
function mountEditor(update: (request: unknown) => object) {
  render(
    <ApiProvider
      rpcTransport={createRouterTransport((r: ConnectRouter) => {
        r.service(ProviderService, {
          listProviderCatalog: () => ({
            providers: [
              {
                id: "custom",
                displayName: "Custom",
                authKinds: [ProviderAuthKind.API_KEY],
              },
            ],
          }),
          listProviderConnections: () => ({ connections: [source] }),
          updateProviderConnection: update,
        });
        r.service(PoolService, { listPools: () => ({}) });
        r.service(RoutingService, { listRoutes: () => ({}) });
      })}
      queryClient={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <ProvidersPage organization={organization} session={session} />
    </ApiProvider>,
  );
}
async function openEditor() {
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: "Edit connection" }),
  );
  const dialog = await screen.findByRole("dialog");
  const advanced = within(dialog)
    .getByText("Advanced connection settings")
    .closest("details") as HTMLDetailsElement;
  return { user, dialog, advanced };
}
const nestedNames = [
  "Endpoints and provider settings",
  "Model API formats",
  "Request compatibility policies",
  "Custom headers",
];
describe("advanced connection settings disclosures", () => {
  it("keeps advanced settings and every nested disclosure collapsed by default", async () => {
    mountEditor(() => ({}));
    const { dialog, advanced, user } = await openEditor();
    expect(advanced.open).toBe(false);
    expect(within(dialog).queryByRole("tab")).toBeNull();
    await user.click(within(dialog).getByText("Advanced connection settings"));
    expect(advanced.open).toBe(true);
    for (const name of nestedNames) {
      const nested = within(advanced)
        .getByText(name, { selector: "summary" })
        .closest("details") as HTMLDetailsElement;
      expect(nested.parentElement).toBe(advanced);
      expect(nested.open).toBe(false);
    }
  });

  it("opens nested disclosures independently from the keyboard-operable summaries", async () => {
    mountEditor(() => ({}));
    const { dialog, advanced, user } = await openEditor();
    await user.click(within(dialog).getByText("Advanced connection settings"));
    await user.click(within(advanced).getByText("Model API formats"));
    const models = within(advanced)
      .getByText("Model API formats")
      .closest("details") as HTMLDetailsElement;
    const endpoints = within(advanced)
      .getByText("Endpoints and provider settings")
      .closest("details") as HTMLDetailsElement;
    expect(models.open).toBe(true);
    expect(endpoints.open).toBe(false);
    expect(within(models).getByDisplayValue("exact-model")).toBeTruthy();
  });

  it("saves through the single footer with unchanged serialization after nested edits", async () => {
    const update = rs.fn((_request: unknown) => ({}));
    mountEditor(update);
    const { dialog, advanced, user } = await openEditor();
    await user.click(within(dialog).getByText("Advanced connection settings"));
    await user.click(
      within(advanced).getByText("Endpoints and provider settings"),
    );
    await user.type(
      screen.getByLabelText("Chat completions endpoint path"),
      "/c",
    );
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(update).toHaveBeenCalledTimes(1);
    const request = update.mock.calls[0]![0] as {
      configuration?: {
        responsesPath?: string;
        chatCompletionsPath?: string;
        modelWires: Record<string, number>;
        policy?: { parallelToolCalls?: boolean };
      };
      resetConfiguration: boolean;
      customHeaders?: unknown;
    };
    expect(request.resetConfiguration).toBe(false);
    expect(request.configuration?.responsesPath).toBe("/generate");
    expect(request.configuration?.chatCompletionsPath).toBe("/c");
    expect(request.configuration?.modelWires).toEqual({
      "exact-model": ProviderWire.CHAT,
    });
    // A displayed false stays an explicit false instead of becoming absent.
    expect(request.configuration?.policy?.parallelToolCalls).toBe(false);
    expect(request.customHeaders).toBeUndefined();
  });

  it("keeps custom headers independent of configuration reset inside their own disclosure", async () => {
    const update = rs.fn((_request: unknown) => ({}));
    mountEditor(update);
    const { dialog, advanced, user } = await openEditor();
    await user.click(within(dialog).getByText("Advanced connection settings"));
    await user.selectOptions(
      screen.getByLabelText("Configuration changes"),
      "reset",
    );
    // Reset hides configuration disclosures but keeps the header disclosure.
    expect(within(advanced).queryByText("Model API formats")).toBeNull();
    expect(
      within(advanced).queryByText("Endpoints and provider settings"),
    ).toBeNull();
    const headers = within(advanced)
      .getByText("Custom headers", { selector: "summary" })
      .closest("details") as HTMLDetailsElement;
    expect(headers.open).toBe(false);
    await user.click(within(advanced).getByText("Custom headers"));
    expect(headers.open).toBe(true);
    expect(within(headers).getByText(/X-Private/)).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    const request = update.mock.calls[0]![0] as {
      resetConfiguration: boolean;
      customHeaders?: unknown;
    };
    expect(request.resetConfiguration).toBe(true);
    expect(request.customHeaders).toBeUndefined();
  });
});
