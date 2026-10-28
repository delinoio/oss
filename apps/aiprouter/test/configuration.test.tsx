// SPDX-License-Identifier: Apache-2.0
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import {
  createRouterTransport,
  ConnectError,
  Code,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  ProviderService,
  PoolService,
  RoutingService,
  HostedSettingsService,
  ProviderConnectionSchema,
  ProviderConfigurationSchema,
  ProviderPolicyOverridesSchema,
  ResponsesItemIdRepairSchema,
  OrganizationSchema,
  GetSessionResponseSchema,
  ProviderAuthKind,
  ProviderWire,
  Role,
  HostedSettingsSchema,
  HostedSearchBackend,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";
import { HostedHelpers } from "../src/hosted";
import type { ReactNode } from "react";
const organization = create(OrganizationSchema, {
  id: "org",
  name: "Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, { userId: "person" });
function mount(ui: ReactNode, routes: (r: ConnectRouter) => void) {
  render(
    <ApiProvider
      rpcTransport={createRouterTransport(routes)}
      queryClient={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      {ui}
    </ApiProvider>,
  );
}
const connection = create(ProviderConnectionSchema, {
  id: "connection",
  providerId: "custom",
  name: "Custom endpoint",
  authKind: ProviderAuthKind.API_KEY,
  configuration: {
    wire: ProviderWire.RESPONSES,
    responsesPath: "/generate",
    policy: { parallelToolCalls: false },
  },
  customHeaderNames: ["X-Private"],
});
function providerRoutes(
  r: ConnectRouter,
  update: (request: unknown) => object,
  source = connection,
) {
  r.service(ProviderService, {
    listProviderCatalog: () => ({
      providers: [
        {
          id: source.providerId,
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
}
describe("provider configuration mutations", () => {
  it.each(["preserve", "omit", "empty", "auto", "reset"] as const)(
    "sends Vertex configuration with %s presence through Connect",
    async (mode) => {
      const user = userEvent.setup();
      const update = rs.fn((_request: unknown) => {
        if (mode === "empty")
          throw new ConnectError(
            "Vertex project must not be empty",
            Code.InvalidArgument,
          );
        return {};
      });
      const source = create(ProviderConnectionSchema, {
        ...connection,
        providerId: "google-vertex",
        configuration: create(ProviderConfigurationSchema, {
          vertexProject: "saved-project",
          vertexLocation: "us-central1",
        }),
      });
      mount(
        <ProvidersPage organization={organization} session={session} />,
        (r) => providerRoutes(r, update, source),
      );
      await user.click(
        await screen.findByRole("button", { name: "Edit connection" }),
      );
      await user.click(screen.getByText("Advanced connection settings"));
      await user.selectOptions(
        screen.getByLabelText("Configuration changes"),
        mode === "reset" ? "reset" : "replace",
      );
      if (mode === "omit") {
        await user.click(screen.getByLabelText("Override vertex project"));
        await user.click(screen.getByLabelText("Override vertex location"));
      } else if (mode === "empty" || mode === "auto") {
        await user.clear(screen.getByLabelText("Vertex project"));
        if (mode === "auto")
          await user.type(screen.getByLabelText("Vertex project"), "auto");
      }
      await user.click(screen.getByRole("button", { name: "Save connection" }));
      expect(update).toHaveBeenCalledTimes(1);
      const request = update.mock.calls[0]![0] as {
        configuration?: { vertexProject?: string; vertexLocation?: string };
        resetConfiguration: boolean;
      };
      if (mode === "reset") {
        expect(request.configuration).toBeUndefined();
        expect(request.resetConfiguration).toBe(true);
      } else {
        expect(request.configuration).toBeDefined();
        expect(request.configuration!.vertexProject).toBe(
          mode === "omit"
            ? undefined
            : mode === "empty"
              ? ""
              : mode === "auto"
                ? "auto"
                : "saved-project",
        );
        expect(request.configuration!.vertexLocation).toBe(
          mode === "omit" ? undefined : "us-central1",
        );
      }
      if (mode === "empty") {
        await screen.findAllByText(
          "Check the entered values and the allowed destination policy, then try again.",
        );
        expect(
          (screen.getByLabelText("Vertex project") as HTMLInputElement).value,
        ).toBe("");
        expect(
          (screen.getByLabelText("Override vertex project") as HTMLInputElement)
            .checked,
        ).toBe(true);
      }
    },
  );

  it("preserves fast false and exact model false overrides during unrelated full configuration replacement", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => ({}));
    const source = create(ProviderConnectionSchema, {
      ...connection,
      configuration: create(ProviderConfigurationSchema, {
        ...connection.configuration!,
        policy: create(ProviderPolicyOverridesSchema, {
          fastEnabled: false,
          modelSupportsServiceTier: { "Case/Model": false },
        }),
      }),
    });
    mount(
      <ProvidersPage organization={organization} session={session} />,
      (r) => providerRoutes(r, update, source),
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit connection" }),
    );
    await user.selectOptions(
      screen.getByLabelText("API format"),
      String(ProviderWire.CHAT),
    );
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      configuration: {
        policy: {
          fastEnabled: false,
          modelSupportsServiceTier: { "Case/Model": false },
        },
      },
    });
  });
  it("edits fast tri-state and model support without lowercasing, rejects duplicate folded IDs", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => ({}));
    mount(
      <ProvidersPage organization={organization} session={session} />,
      (r) => providerRoutes(r, update),
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit connection" }),
    );
    await user.click(screen.getByText("Advanced connection settings"));
    await user.click(screen.getByText("Request compatibility policies"));
    await user.selectOptions(
      screen.getByLabelText("Fast mode policy"),
      "false",
    );
    await user.click(
      screen.getByRole("button", { name: "Add service-tier override" }),
    );
    await user.type(
      screen.getByLabelText("Service-tier model ID 1"),
      "Case/Model",
    );
    await user.click(
      screen.getByRole("button", { name: "Add service-tier override" }),
    );
    await user.type(
      screen.getByLabelText("Service-tier model ID 2"),
      "case/model",
    );
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(update).not.toHaveBeenCalled();
    await screen.findByText(
      "Service-tier model IDs must be nonempty and unique, ignoring letter case.",
    );
    await user.click(
      screen.getByRole("button", { name: "Remove service-tier override 2" }),
    );
    await user.selectOptions(screen.getByLabelText("Fast mode policy"), "");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    const request = update.mock.calls[0]?.[0] as {
      configuration: {
        policy: {
          fastEnabled?: boolean;
          modelSupportsServiceTier: Record<string, boolean>;
        };
      };
    };
    expect(request.configuration.policy.fastEnabled).toBeUndefined();
    expect(request.configuration.policy.modelSupportsServiceTier).toEqual({
      "Case/Model": false,
    });
  });

  it.each(["inherit", "empty", "custom"] as const)(
    "sends nested item ID repair policy with %s semantics",
    async (mode) => {
      const user = userEvent.setup();
      const update = rs.fn((_request: unknown) => ({}));
      const source = create(ProviderConnectionSchema, {
        ...connection,
        configuration: create(ProviderConfigurationSchema, {
          ...connection.configuration!,
          policy: create(ProviderPolicyOverridesSchema, {
            responsesItemIdRepair: create(ResponsesItemIdRepairSchema, {
              message: ["old-placeholder"],
              repairInvalidIds: true,
            }),
          }),
        }),
      });
      mount(
        <ProvidersPage organization={organization} session={session} />,
        (r) => providerRoutes(r, update, source),
      );
      await user.click(
        await screen.findByRole("button", { name: "Edit connection" }),
      );
      await user.click(screen.getByText("Advanced connection settings"));
      await user.click(screen.getByText("Request compatibility policies"));
      expect(
        (
          screen.getByLabelText(
            "Message placeholder IDs",
          ) as HTMLTextAreaElement
        ).value,
      ).toBe("old-placeholder");
      await user.selectOptions(
        screen.getByLabelText("Item ID repair policy"),
        "inherit",
      );
      if (mode !== "inherit")
        await user.selectOptions(
          screen.getByLabelText("Item ID repair policy"),
          "replace",
        );
      if (mode === "custom") {
        await user.type(
          screen.getByLabelText("Message placeholder IDs"),
          "placeholder-a\nplaceholder-b",
        );
        await user.type(
          screen.getByLabelText("Reasoning placeholder IDs"),
          "reasoning-placeholder",
        );
        await user.selectOptions(
          screen.getByLabelText("Repair invalid IDs"),
          "false",
        );
      }
      await user.click(screen.getByRole("button", { name: "Save connection" }));
      const request = update.mock.calls[0]?.[0] as {
        configuration: {
          policy: {
            responsesItemIdRepair?: {
              message: string[];
              reasoning: string[];
              repairInvalidIds?: boolean;
              repairMissingTerminalIds?: boolean;
            };
          };
        };
      };
      const repair = request.configuration.policy.responsesItemIdRepair;
      if (mode === "inherit") expect(repair).toBeUndefined();
      else {
        expect(repair?.message).toEqual(
          mode === "custom" ? ["placeholder-a", "placeholder-b"] : [],
        );
        expect(repair?.reasoning).toEqual(
          mode === "custom" ? ["reasoning-placeholder"] : [],
        );
        expect(repair?.repairInvalidIds).toBe(
          mode === "custom" ? false : undefined,
        );
        expect(repair?.repairMissingTerminalIds).toBeUndefined();
      }
    },
  );

  it("preserves hidden header values and configuration when only the name changes", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => ({}));
    mount(
      <ProvidersPage organization={organization} session={session} />,
      (r) => providerRoutes(r, update),
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit connection" }),
    );
    await user.type(screen.getByLabelText("Account label"), " renamed");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      name: "Custom endpoint renamed",
      resetConfiguration: false,
    });
    expect(
      (update.mock.calls[0]?.[0] as { configuration?: unknown }).configuration,
    ).toBeUndefined();
    expect(
      (update.mock.calls[0]?.[0] as { customHeaders?: unknown }).customHeaders,
    ).toBeUndefined();
  });
  it("sends typed exact model formats, explicit false policy, and masked replacement headers", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => ({}));
    mount(
      <ProvidersPage organization={organization} session={session} />,
      (r) => providerRoutes(r, update),
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit connection" }),
    );
    await user.click(screen.getByText("Advanced connection settings"));
    await user.selectOptions(
      screen.getByLabelText("API format"),
      String(ProviderWire.ANTHROPIC),
    );
    await user.click(
      screen.getByRole("button", { name: "Add model override" }),
    );
    await user.type(screen.getByLabelText("Model ID 1"), "exact-model");
    expect(
      within(screen.getByLabelText("Model format 1")).queryByRole("option", {
        name: "Google",
      }),
    ).toBeNull();
    expect(
      within(screen.getByLabelText("Model format 1")).queryByRole("option", {
        name: "Anthropic",
      }),
    ).toBeNull();
    await user.selectOptions(
      screen.getByLabelText("Model format 1"),
      String(ProviderWire.RESPONSES),
    );
    await user.click(screen.getByText("Request compatibility policies"));
    await user.selectOptions(
      screen.getByLabelText("Prompt cache key"),
      "false",
    );
    await user.selectOptions(
      screen.getByLabelText("Header changes"),
      "replace",
    );
    await user.click(screen.getByRole("button", { name: "Add header" }));
    await user.type(screen.getByLabelText("Header name 1"), "X-Private");
    const secret = screen.getByLabelText("Header value 1");
    expect(secret.getAttribute("type")).toBe("password");
    await user.type(secret, "private-test-value");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org",
      configuration: {
        wire: ProviderWire.ANTHROPIC,
        responsesPath: "/generate",
        modelWires: { "exact-model": ProviderWire.RESPONSES },
        policy: { parallelToolCalls: false, promptCacheKey: false },
      },
      customHeaders: { values: { "X-Private": "private-test-value" } },
    });
  });
  it("resets configuration separately from explicit header clearing", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => ({}));
    mount(
      <ProvidersPage organization={organization} session={session} />,
      (r) => providerRoutes(r, update),
    );
    await user.click(
      await screen.findByRole("button", { name: "Edit connection" }),
    );
    await user.click(screen.getByText("Advanced connection settings"));
    await user.selectOptions(
      screen.getByLabelText("Configuration changes"),
      "reset",
    );
    await user.selectOptions(screen.getByLabelText("Header changes"), "clear");
    await user.click(screen.getByRole("button", { name: "Save connection" }));
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      resetConfiguration: true,
      customHeaders: { values: {} },
    });
    expect(
      (update.mock.calls[0]?.[0] as { configuration?: unknown }).configuration,
    ).toBeUndefined();
  });
});
describe("personal hosted settings", () => {
  const settings = create(HostedSettingsSchema, {
    search: {
      enabled: false,
      backend: HostedSearchBackend.XAI,
      maxSearchesPerTurn: 0,
      target: {
        selection: { case: "connectionId", value: "connection" },
        modelId: "grok",
      },
    },
    images: {
      bridgeEnabled: false,
      videoBridgeEnabled: true,
      target: {
        selection: { case: "poolId", value: "pool" },
        modelId: "media",
      },
    },
  });
  function routes(r: ConnectRouter, update: (request: unknown) => object) {
    r.service(HostedSettingsService, {
      getHostedSettings: () => ({ settings }),
      updateHostedSettings: update,
    });
    r.service(ProviderService, {
      listProviderConnections: () => ({ connections: [connection] }),
    });
    r.service(PoolService, {
      listPools: () => ({ pools: [{ id: "pool", name: "Media" }] }),
    });
    r.service(RoutingService, { listRoutes: () => ({}) });
  }
  it("preserves false, zero and unrelated sections while replacing complete settings", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => ({ settings }));
    mount(<HostedHelpers organizationId="org" />, (r) => routes(r, update));
    await user.click(
      screen.getByRole("button", { name: "Configure hosted helpers" }),
    );
    await screen.findByLabelText("Search backend");
    await user.type(screen.getByLabelText("Search model ID"), "-updated");
    await user.click(
      screen.getByRole("button", { name: "Save helper settings" }),
    );
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org",
      settings: {
        search: {
          enabled: false,
          maxSearchesPerTurn: 0,
          target: { modelId: "grok-updated" },
        },
        images: {
          videoBridgeEnabled: true,
          target: { selection: { case: "poolId", value: "pool" } },
        },
      },
    });
    expect(
      (update.mock.calls[0]?.[0] as { settings: { vision?: unknown } }).settings
        .vision,
    ).toBeUndefined();
  });
  it("retains failed drafts and sends routed vision without a connection target", async () => {
    const user = userEvent.setup(),
      update = rs.fn((_request: unknown) => {
        throw new ConnectError("invalid target", Code.InvalidArgument);
      });
    mount(<HostedHelpers organizationId="org" />, (r) => routes(r, update));
    await user.click(
      screen.getByRole("button", { name: "Configure hosted helpers" }),
    );
    await user.click(
      await screen.findByLabelText("Configure image understanding"),
    );
    await user.selectOptions(screen.getByLabelText("Vision backend"), "3");
    await user.type(
      screen.getByLabelText("Vision route model"),
      "vision-route",
    );
    await user.click(
      screen.getByRole("button", { name: "Save helper settings" }),
    );
    await screen.findByRole("alert");
    expect(
      (screen.getByLabelText("Vision route model") as HTMLInputElement).value,
    ).toBe("vision-route");
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      settings: {
        vision: { selection: { case: "routeModel", value: "vision-route" } },
      },
    });
    expect(
      within(screen.getByRole("dialog")).queryByLabelText("Vision account"),
    ).toBeNull();
  });
});
describe("hosted helpers dialog shell", () => {
  type Handlers = {
    get?: () => object;
    update?: (request: unknown) => object | Promise<object>;
    connections?: () => object;
    pools?: () => object;
  };
  function shellRoutes(r: ConnectRouter, handlers: Handlers = {}) {
    r.service(HostedSettingsService, {
      getHostedSettings: handlers.get ?? (() => ({})),
      updateHostedSettings: handlers.update ?? (() => ({})),
    });
    r.service(ProviderService, {
      listProviderConnections:
        handlers.connections ?? (() => ({ connections: [connection] })),
    });
    r.service(PoolService, {
      listPools: handlers.pools ?? (() => ({ pools: [] })),
    });
    r.service(RoutingService, { listRoutes: () => ({}) });
  }
  async function openDialog(user: ReturnType<typeof userEvent.setup>) {
    await user.click(
      screen.getByRole("button", { name: "Configure hosted helpers" }),
    );
  }
  it("names the dialog, focuses its heading and shows scope copy and the replacement notice", async () => {
    const user = userEvent.setup();
    mount(
      <HostedHelpers organizationId="org" organizationName="Studio" />,
      (r) => shellRoutes(r),
    );
    await openDialog(user);
    const dialog = await screen.findByRole("dialog", {
      name: "Hosted helpers",
    });
    await within(dialog).findByLabelText("Configure web search");
    const heading = within(dialog).getByRole("heading", {
      name: "Hosted helpers",
      level: 2,
    });
    expect(document.activeElement).toBe(heading);
    expect(within(dialog).getByText("Only yours in Studio.")).toBeTruthy();
    expect(
      within(dialog).getByText(
        "Helpers use their own selected accounts, not the account serving the main request.",
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    const notice = within(dialog).getByRole("heading", {
      name: "Save replaces all helper settings",
    });
    const save = within(dialog).getByRole("button", {
      name: "Save helper settings",
    });
    expect(
      notice.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      within(dialog).queryByText(
        "Save replaces all helper settings. Removing a configuration does not always disable the provider default.",
      ),
    ).toBeNull();
    await user.click(within(dialog).getByLabelText("Configure web search"));
    await user.click(within(dialog).getByText("Advanced search settings"));
    expect(
      within(dialog).getByText(
        "Save replaces all helper settings. Removing a configuration does not always disable the provider default.",
      ),
    ).toBeTruthy();
  });
  it("shows the loading label and a retryable load failure without an editor", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, {
        get: async () => {
          attempts += 1;
          if (attempts === 1) {
            await gate;
            throw new ConnectError("down", Code.Unavailable);
          }
          return {};
        },
      }),
    );
    await openDialog(user);
    expect(screen.getByText("Loading hosted helper settings…")).toBeTruthy();
    release();
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("button", { name: "Save helper settings" }),
    ).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByLabelText("Configure web search");
    expect(
      screen.getByRole("button", { name: "Save helper settings" }),
    ).toBeTruthy();
  });
  it("blocks Save with a visible label while resources load and after they fail, then retries without losing the draft", async () => {
    const user = userEvent.setup();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    let poolCalls = 0;
    const update = rs.fn((_request: unknown) => ({}));
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, {
        update,
        pools: async () => {
          poolCalls += 1;
          if (poolCalls === 1) {
            await gate;
            throw new ConnectError("pools down", Code.Unavailable);
          }
          return { pools: [{ id: "pool", name: "Media" }] };
        },
      }),
    );
    await openDialog(user);
    await screen.findByLabelText("Configure web search");
    expect(
      screen.getByText(
        "Loading personal connections and pools… Save is unavailable until they load.",
      ),
    ).toBeTruthy();
    const save = screen.getByRole("button", { name: "Save helper settings" });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    await user.click(screen.getByLabelText("Configure web search"));
    release();
    await screen.findByText(
      "Personal connections and pools could not be loaded. Save is unavailable until they load.",
    );
    expect(
      screen.queryByText("No personal connections or pools available."),
    ).toBeNull();
    expect((save as HTMLButtonElement).disabled).toBe(true);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByRole("option", { name: "Pool: Media" });
    expect((save as HTMLButtonElement).disabled).toBe(false);
    expect(
      (screen.getByLabelText("Configure web search") as HTMLInputElement)
        .checked,
    ).toBe(true);
    await user.click(save);
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org",
      settings: { search: {} },
    });
  });
  it("reports a failed settings refresh over cached data, blocks Save, and keeps the draft through retry", async () => {
    const user = userEvent.setup();
    let calls = 0;
    const update = rs.fn((_request: unknown) => ({}));
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, {
        update,
        get: () => {
          calls += 1;
          // Only the refetch on reopen fails; the cache still holds call 1.
          if (calls === 2) throw new ConnectError("down", Code.Unavailable);
          return {};
        },
      }),
    );
    await openDialog(user);
    await screen.findByLabelText("Configure web search");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await openDialog(user);
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText(
      "Hosted helper settings could not be refreshed, so the settings shown may be out of date. Save is unavailable until they load.",
    );
    expect(within(dialog).getByRole("alert")).toBeTruthy();
    const save = within(dialog).getByRole("button", {
      name: "Save helper settings",
    }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await user.click(within(dialog).getByLabelText("Configure web search"));
    expect(save.disabled).toBe(true);
    await user.click(within(dialog).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(save.disabled).toBe(false));
    expect(within(dialog).queryByRole("alert")).toBeNull();
    expect(
      (
        within(dialog).getByLabelText(
          "Configure web search",
        ) as HTMLInputElement
      ).checked,
    ).toBe(true);
    await user.click(save);
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org",
      settings: { search: {} },
    });
  });
  it("distinguishes no accounts from a resource error", async () => {
    const user = userEvent.setup();
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, { connections: () => ({ connections: [] }) }),
    );
    await openDialog(user);
    await screen.findByText("No personal connections or pools available.");
    expect(screen.queryByRole("alert")).toBeNull();
    expect(
      (
        screen.getByRole("button", {
          name: "Save helper settings",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });
  it("reports the outcome of a save in the entry region after the dialog closes mid-save", async () => {
    const user = userEvent.setup();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => (release = resolve));
    const update = rs.fn(async (_request: unknown) => {
      await gate;
      return {};
    });
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, { update }),
    );
    await openDialog(user);
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByLabelText("Configure web search");
    const save = within(dialog).getByRole("button", {
      name: "Save helper settings",
    });
    await user.click(save);
    await user.click(save);
    expect(update).toHaveBeenCalledTimes(1);
    expect(
      within(dialog).getByText(
        "Saving… Closing this dialog does not cancel a submitted change.",
      ),
    ).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    const entry = screen.getByRole("status");
    expect(entry.textContent).toBe(
      "Saving… Closing this dialog does not cancel a submitted change.",
    );
    release();
    await screen.findByText("Hosted helper settings saved.");
    expect(update).toHaveBeenCalledTimes(1);
  });
  it("reports a rejected save in the entry region after close without a success claim", async () => {
    const user = userEvent.setup();
    let fail: () => void = () => {};
    const gate = new Promise<void>((resolve) => (fail = resolve));
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, {
        update: async () => {
          await gate;
          throw new ConnectError("invalid", Code.InvalidArgument);
        },
      }),
    );
    await openDialog(user);
    await screen.findByLabelText("Configure web search");
    await user.click(
      screen.getByRole("button", { name: "Save helper settings" }),
    );
    await user.click(screen.getByRole("button", { name: "Close dialog" }));
    fail();
    await screen.findByRole("alert");
    expect(screen.queryByText("Hosted helper settings saved.")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });
  it("keeps the dialog and draft open with an error when a save is rejected", async () => {
    const user = userEvent.setup();
    mount(<HostedHelpers organizationId="org" />, (r) =>
      shellRoutes(r, {
        update: () => {
          throw new ConnectError("invalid", Code.InvalidArgument);
        },
      }),
    );
    await openDialog(user);
    await user.click(await screen.findByLabelText("Configure web search"));
    await user.click(
      screen.getByRole("button", { name: "Save helper settings" }),
    );
    const dialog = screen.getByRole("dialog");
    await within(dialog).findByRole("alert");
    expect(
      within(dialog).getByRole("button", { name: "Save helper settings" }),
    ).toBeTruthy();
    expect(
      (
        within(dialog).getByLabelText(
          "Configure web search",
        ) as HTMLInputElement
      ).checked,
    ).toBe(true);
    expect(screen.queryByText("Hosted helper settings saved.")).toBeNull();
  });
});
