// SPDX-License-Identifier: Apache-2.0
// Protocol 2 retires Model-resource discovery/revision fixtures. These cases
// cover inline source/native IDs and retain account, draft and receipt guards.
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  KnownSubscriptionModelCatalogSource,
  ProviderInventoryCapability,
  ProviderService,
  ResourceSchema,
  ResourceService,
  SubscriptionServiceIdentity,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
  type SaveAgentWorkerRequest,
  type GetResourceRequest,
  type ListResourcesRequest,
  type ListEndpointModelsRequest,
} from "@delinoio/delidev-api-client";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { MutationIntents } from "./mutation";
import { document, encode } from "./documents";
import { chooseScrollOption } from "./test-scroll-picker";
function fixture(supported = true) {
  const row = (
    kind: EntityKind,
    data: Record<string, unknown>,
    schemaVersion = 1,
  ) =>
    create(ResourceSchema, {
      kind,
      schemaVersion,
      id: newRequestId(),
      revision: 1n,
      documentJson: encode(data),
    });
  const provider = row(EntityKind.PROVIDER, {
    name: "Original API",
    protocol: "openai-responses",
    endpoint: "https://fixture.test/v1",
    enabled: true,
    authentication: "keyless",
  });
  const accounts = ["First account", "Second account"].map((alias) =>
    row(EntityKind.ACCOUNT, {
      alias,
      type: "api",
      provider_id: provider.id,
      health: "unverified",
      enabled: true,
      connection: { id: newRequestId(), authentication: "keyless" },
    }),
  );
  const subscription = row(
    EntityKind.ACCOUNT,
    {
      alias: "Original subscription",
      type: "subscription",
      subscription_service: "chatgpt",
      health: "ready",
      enabled: true,
      connection: { id: newRequestId(), authentication: "subscription" },
    },
    2,
  );
  const records = [provider, ...accounts, subscription];
  const get = vi.fn((request: GetResourceRequest) => ({
    resource: records.find((row) => row.id === request.id),
  }));
  const list = vi.fn((request: ListResourcesRequest) => ({
    resources:
      request.filter?.kind === EntityKind.ACCOUNT
        ? request.subscriptionService === SubscriptionServiceIdentity.CHATGPT
          ? [subscription]
          : request.providerId === provider.id
            ? accounts
            : []
        : [],
  }));
  const endpoint = vi.fn(async (request: ListEndpointModelsRequest) => {
    const account = accounts.find((row) => row.id === request.accountId)!;
    return {
      accountId: account.id,
      accountRevision: account.revision,
      providerId: provider.id,
      providerRevision: provider.revision,
      connectionId: String(
        (document(account).connection as Record<string, unknown>).id,
      ),
      models: [
        {
          nativeId: "exact-one",
          displayName: "Endpoint One",
          inputModalities: ["text"],
        },
        {
          nativeId: "exact-two",
          displayName: "Endpoint Two",
          inputModalities: ["text", "image"],
        },
      ],
    };
  });
  const known = vi.fn(() => ({
    subscriptionService: SubscriptionServiceIdentity.CHATGPT,
    models: [{ nativeId: "gpt-exact", displayName: "Known GPT" }],
    catalogVersion: `sha256:${"a".repeat(64)}`,
    updatedAt: "2026-10-06",
    source: KnownSubscriptionModelCatalogSource.BUNDLED,
  }));
  const save = vi.fn(async (request: SaveAgentWorkerRequest) => ({
    requestId: request.mutation!.requestId,
    resource: row(
      EntityKind.AGENT,
      JSON.parse(new TextDecoder().decode(request.documentJson)),
      4,
    ),
  }));
  // Every RPC the wizard starts, by method name, in start order.
  const rpc: string[] = [];
  const transport = createRouterTransport(
    (router) => {
      router.service(SystemService, {
        getStatus: () => ({
          protocolVersion: 2,
          capabilities: supported
            ? [
                SystemCapability.INLINE_WORKER_MODELS_V1,
                SystemCapability.KNOWN_SUBSCRIPTION_MODELS_V1,
              ]
            : [],
        }),
      });
      router.service(ProviderService, {
        listProviderInventory: () => ({
          entries: [
            {
              provider,
              providerId: provider.id,
              displayName: "Original API",
              enabled: true,
            },
          ],
          capabilities: [
            ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
            ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
            ProviderInventoryCapability.COMPATIBLE_ACCOUNT_API_PROTOCOL_FILTER_V1,
          ],
        }),
        listEndpointModels: endpoint,
        listKnownSubscriptionModels: known,
      });
      router.service(ResourceService, {
        listResources: list,
        getResource: get,
      });
      router.service(ConfigurationService, { saveAgentWorker: save });
    },
    {
      transport: {
        interceptors: [
          (next) => (request) => {
            rpc.push(request.method.name);
            return next(request);
          },
        ],
      },
    },
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const saved = vi.fn(),
    cancel = vi.fn();
  const view = (active = true, initial?: Resource) => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <MutationIntents>
            <AgentWorkerWizard
              active={active}
              initial={initial}
              saved={saved}
              cancel={cancel}
            />
          </MutationIntents>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    row,
    provider,
    accounts,
    subscription,
    records,
    get,
    list,
    endpoint,
    known,
    rpc,
    save,
    saved,
    cancel,
    view,
    client,
  };
}
async function accounts(
  f: ReturnType<typeof fixture>,
  subscription = false,
  multi = false,
) {
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  const picker = screen.getByRole("combobox", { name: "Account source 1" });
  await chooseScrollOption(
    picker,
    subscription ? "subscription:chatgpt" : `api:${f.provider.id}`,
  );
  const rows = subscription
    ? [f.subscription]
    : multi
      ? f.accounts
      : [f.accounts[0]];
  for (const row of rows)
    fireEvent.click(
      await screen.findByRole("checkbox", {
        name: new RegExp(String(document(row).alias)),
      }),
    );
  await waitFor(() => {
    for (const row of rows)
      expect(
        f.client
          .getQueryCache()
          .getAll()
          .some(
            (q) =>
              (q.state.data as { resource?: Resource } | undefined)?.resource
                ?.id === row.id &&
              q.state.status === "success" &&
              q.state.fetchStatus === "idle",
          ),
      ).toBe(true);
  });
  await act(async () => {});
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Model", level: 3 });
  return screen.getByRole("combobox", {
    name: /^Model for /,
  }) as HTMLInputElement;
}
async function configure(input: HTMLInputElement, id = "typed-exact") {
  fireEvent.change(input, { target: { value: id } });
  fireEvent.keyDown(input, { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Configure", level: 3 });
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Inline Worker" },
  });
}
it.each(["Codex", "Claude Code", "OpenCode", "Grok Build"])(
  "confirms %s and focuses Accounts without saved Model reads",
  async (name) => {
    const f = fixture();
    render(f.view());
    fireEvent.click(await screen.findByRole("radio", { name }));
    expect(screen.getByRole("heading", { name: "Accounts", level: 3 })).toBe(
      globalThis.document.activeElement,
    );
    expect(f.endpoint).not.toHaveBeenCalled();
    expect(f.known).not.toHaveBeenCalled();
    expect(
      f.list.mock.calls.every(
        ([request]) => request.filter?.kind !== EntityKind.MODEL,
      ),
    ).toBe(true);
    expect(f.save).not.toHaveBeenCalled();
  },
);
it("keeps unsupported protocol support unavailable", async () => {
  const f = fixture(false);
  render(f.view());
  await screen.findByText(/Update.*server/);
  expect(screen.queryByRole("radio")).toBeNull();
  expect(f.save).not.toHaveBeenCalled();
});
it("gates inactive harness confirmation", async () => {
  const f = fixture();
  const view = render(f.view());
  await screen.findByRole("radio", { name: "Codex" });
  view.rerender(f.view(false));
  expect(screen.getByRole("radio", { name: "Codex" })).toHaveProperty(
    "disabled",
    true,
  );
  expect(f.save).not.toHaveBeenCalled();
});
it("moves harness focus with keyboard without confirming", async () => {
  const f = fixture();
  render(f.view());
  const codex = await screen.findByRole("radio", { name: "Codex" });
  codex.focus();
  fireEvent.keyDown(codex, { key: "ArrowRight" });
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("radio", { name: "Claude Code" }),
  );
  expect(
    screen.getByRole("heading", { name: "Harness", level: 3 }),
  ).toBeTruthy();
  expect(f.save).not.toHaveBeenCalled();
});
it("saves ordered accounts and source-native model atomically only at Configure", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f, false, true);
  expect(f.save).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  fireEvent.focus(input);
  fireEvent.click(await screen.findByRole("option", { name: /Endpoint Two/ }));
  await configure(input, "exact-two");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const request = f.save.mock.calls[0][0];
  expect(request.schemaVersion).toBe(4);
  expect(request.model).toBeUndefined();
  expect(request.routeModels).toMatchObject([
    { selection: { case: "nativeId", value: "exact-two" } },
  ]);
  const data = JSON.parse(new TextDecoder().decode(request.documentJson));
  expect(data.routes[0]).toMatchObject({
    model: { provider_id: f.provider.id, native_id: "exact-two" },
    accounts: f.accounts.map((row) => ({ id: row.id, weight: 1 })),
  });
  expect(data.model_id).toBeUndefined();
  expect(data.accounts).toBeUndefined();
});
it("accepts a typed exact native ID with failed endpoint hints", async () => {
  const f = fixture();
  f.endpoint.mockRejectedValue(
    new ConnectError("Fixture read unavailable", Code.Unavailable),
  );
  render(f.view());
  const input = await accounts(f);
  expect(f.endpoint).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  await waitFor(() => expect(f.endpoint).toHaveBeenCalledTimes(1));
  await configure(input, "unlisted-exact-ID");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  expect(f.save.mock.calls[0][0].routeModels[0].selection).toEqual({
    case: "nativeId",
    value: "unlisted-exact-ID",
  });
  expect(
    f.list.mock.calls.every(
      ([request]) => request.filter?.kind !== EntityKind.MODEL,
    ),
  ).toBe(true);
});
it("keeps subscription suggestions advisory and saves original service/native identity", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f, true);
  fireEvent.focus(input);
  fireEvent.click(await screen.findByRole("option", { name: /Known GPT/ }));
  await configure(input, "gpt-exact");
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const route = JSON.parse(
    new TextDecoder().decode(f.save.mock.calls[0][0].documentJson),
  ).routes[0];
  expect(route.model).toMatchObject({
    subscription_service: "chatgpt",
    native_id: "gpt-exact",
  });
  expect(route.model.provider_id).toBeUndefined();
  expect(f.endpoint).not.toHaveBeenCalled();
});
it("retains Configure draft through Back and active visibility changes", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = await accounts(f);
  await configure(input);
  view.rerender(f.view(false));
  view.rerender(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(screen.getByRole("combobox", { name: /^Model for / })).toHaveProperty(
    "value",
    "typed-exact",
  );
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByLabelText("Name")).toHaveProperty(
    "value",
    "Inline Worker",
  );
});
it("blocks invalid exact IDs before save and reveals Model", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  fireEvent.change(input, { target: { value: "x".repeat(257) } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("heading", { name: "Model", level: 3 })).toBeTruthy();
  expect(f.save).not.toHaveBeenCalled();
});
it("ignores composing Enter in the native model picker", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  fireEvent.change(input, { target: { value: "original" } });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  fireEvent.keyDown(input, { key: "Enter", isComposing: true });
  expect(input).toHaveProperty("value", "original");
  expect(screen.getByRole("heading", { name: "Model", level: 3 })).toBeTruthy();
  expect(f.save).not.toHaveBeenCalled();
});
it("rejects endpoint metadata from another original account", async () => {
  const f = fixture();
  f.endpoint.mockImplementation(async () => ({
    accountId: newRequestId(),
    accountRevision: 1n,
    providerId: f.provider.id,
    providerRevision: 1n,
    connectionId: newRequestId(),
    models: [
      {
        nativeId: "foreign",
        displayName: "Foreign model",
        inputModalities: ["text"],
      },
    ],
  }));
  render(f.view());
  const input = await accounts(f);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  fireEvent.focus(input);
  await screen.findByText(/catalog page includes unsupported/);
  expect(screen.queryByRole("option", { name: /Foreign model/ })).toBeNull();
  expect(f.save).not.toHaveBeenCalled();
});
it("preserves the exact uncertain atomic request across a hidden visit", async () => {
  const f = fixture();
  f.save.mockRejectedValueOnce(
    new ConnectError("Lost receipt", Code.Unavailable),
  );
  const view = render(f.view());
  const input = await accounts(f);
  await configure(input);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("button", { name: "Retry the same Worker save" });
  const original = f.save.mock.calls[0][0];
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(
    screen.getByRole("button", { name: "Save Agent Worker" }),
  ).toHaveProperty("disabled", true);
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same Worker save" }),
  );
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(2));
  expect(f.save.mock.calls[1][0]).toEqual(original);
});

it("reads API endpoint hints only after explicit Refresh models and never on Model entry or cache invalidation", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = await accounts(f);
  expect(f.endpoint).not.toHaveBeenCalled();
  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "typed-native" } });
  expect(f.endpoint).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "" } });
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  await waitFor(() => expect(f.endpoint).toHaveBeenCalledTimes(1));
  await screen.findByRole("option", { name: /Endpoint One/ });
  await f.client.invalidateQueries();
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(f.endpoint).toHaveBeenCalledTimes(1);
  expect(f.save).not.toHaveBeenCalled();
});

it("preserves complete multimodal advisory metadata without new model authority", async () => {
  const f = fixture(),
    modalities = ["text", "image", "audio", "video", "file"];
  const original = f.endpoint.getMockImplementation()!;
  f.endpoint.mockImplementation(async (request) => {
    const response = await original(request);
    response.models[1].inputModalities = modalities;
    return response;
  });
  render(f.view());
  const input = await accounts(f);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  fireEvent.focus(input);
  await screen.findByRole("option", { name: /Endpoint One/ });
  fireEvent.click(await screen.findByRole("option", { name: /Endpoint Two/ }));
  expect(input.value).toBe("exact-two");
  // Advance without editing the selected ID, retaining its suggestion metadata.
  fireEvent.keyDown(input, { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Configure", level: 3 });
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Multimodal Worker" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const request = f.save.mock.calls[0][0],
    route = JSON.parse(new TextDecoder().decode(request.documentJson))
      .routes[0];
  expect(request.routeModels[0].selection).toEqual({
    case: "nativeId",
    value: "exact-two",
  });
  expect(route.model).toMatchObject({
    provider_id: f.provider.id,
    native_id: "exact-two",
    input_modalities: modalities,
    metadata_source: "user-declared",
  });
  expect(
    f.list.mock.calls.every(
      ([request]) => request.filter?.kind !== EntityKind.MODEL,
    ),
  ).toBe(true);
});

it.each([
  ["too many tokens", Array(17).fill("text")],
  ["empty token", [""]],
  ["oversized token", ["a".repeat(33)]],
  ["uppercase", ["Audio"]],
  ["digit", ["audio2"]],
  ["underscore", ["audio_input"]],
  ["Unicode", ["오디오"]],
  ["whitespace", ["audio "]],
] as [string, string[]][])(
  "rejects an entire endpoint response with %s",
  async (_name, modalities) => {
    const f = fixture(),
      original = f.endpoint.getMockImplementation()!;
    f.endpoint.mockImplementation(async (request) => {
      const response = await original(request);
      response.models[1].inputModalities = modalities;
      return response;
    });
    render(f.view());
    const input = await accounts(f);
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh models from endpoint" }),
    );
    fireEvent.focus(input);
    await screen.findByText(/catalog page includes unsupported/);
    expect(screen.queryByRole("option", { name: /Endpoint One/ })).toBeNull();
    expect(f.save).not.toHaveBeenCalled();
  },
);

it.each([
  "accountId",
  "providerId",
  "connectionId",
  "accountRevision",
  "providerRevision",
  "duplicateId",
  "oversizedId",
])("retains endpoint rejection for %s", async (field) => {
  const f = fixture(),
    original = f.endpoint.getMockImplementation()!;
  f.endpoint.mockImplementation(async (request) => {
    const response = await original(request);
    if (
      field === "accountId" ||
      field === "providerId" ||
      field === "connectionId"
    )
      response[field] = newRequestId();
    else if (field === "accountRevision" || field === "providerRevision")
      response[field] = 0n;
    else
      response.models[1].nativeId =
        field === "duplicateId" ? response.models[0].nativeId : "x".repeat(257);
    return response;
  });
  render(f.view());
  const input = await accounts(f);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  fireEvent.focus(input);
  await screen.findByText(/catalog page includes unsupported/);
  expect(screen.queryByRole("option", { name: /Endpoint One/ })).toBeNull();
  expect(f.save).not.toHaveBeenCalled();
});
async function selectFirstAccount(f: ReturnType<typeof fixture>) {
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Account source 1" }),
    `api:${f.provider.id}`,
  );
  const row = f.accounts[0];
  const rows = await screen.findAllByRole("checkbox");
  fireEvent.click(rows[0]);
  await waitFor(() =>
    expect(
      f.client
        .getQueryCache()
        .getAll()
        .some(
          (q) =>
            (q.state.data as { resource?: Resource } | undefined)?.resource
              ?.id === row.id &&
            q.state.status === "success" &&
            q.state.fetchStatus === "idle",
        ),
    ).toBe(true),
  );
  await act(async () => {});
}
function selectedAccountNames() {
  return screen
    .getAllByRole("checkbox", { checked: true })
    .map((box) => box.getAttribute("aria-label"));
}
it("derives the nameless selected account fallback at render time across a poll failure and a language change", async () => {
  const { i18n } = await import("./localization");
  const f = fixture();
  const row = f.accounts[0];
  row.documentJson = encode({ ...document(row), alias: undefined });
  render(f.view());
  await selectFirstAccount(f);
  expect(selectedAccountNames()).toEqual([
    expect.stringMatching(/^Select Account unavailable · \d+$/),
  ]);
  const english = selectedAccountNames()[0]!;
  const original = f.get.getMockImplementation()!;
  f.get.mockImplementation(() => {
    throw new ConnectError("Fixture poll unavailable", Code.Unavailable);
  });
  await act(async () => {
    await f.client.refetchQueries({ type: "active" });
  });
  await waitFor(() => expect(f.get).toHaveBeenCalled());
  expect(selectedAccountNames()).toEqual([english]);
  try {
    await act(() => i18n.changeLanguage("ko"));
    expect(selectedAccountNames()).toEqual([
      expect.stringMatching(/^계정 정보 없음 · \d+ 선택$/),
    ]);
  } finally {
    await act(() => i18n.changeLanguage("en"));
  }
  expect(selectedAccountNames()).toEqual([english]);
  f.get.mockImplementation(original);
});
it("retains a genuine alias equal to a generated fallback byte for byte after a poll failure", async () => {
  const f = fixture();
  const row = f.accounts[0];
  const alias = "Account unavailable · 9";
  row.documentJson = encode({ ...document(row), alias });
  render(f.view());
  await selectFirstAccount(f);
  expect(selectedAccountNames()).toEqual([`Select ${alias}`]);
  f.get.mockImplementation(() => {
    throw new ConnectError("Fixture poll unavailable", Code.Unavailable);
  });
  await act(async () => {
    await f.client.refetchQueries({ type: "active" });
  });
  expect(selectedAccountNames()).toEqual([`Select ${alias}`]);
});
it("keeps name precedence over alias in the retained selected account name", async () => {
  const f = fixture();
  const row = f.accounts[0];
  row.documentJson = encode({
    ...document(row),
    name: "Named",
    alias: "Other",
  });
  render(f.view());
  await selectFirstAccount(f);
  expect(selectedAccountNames()).toEqual(["Select Named"]);
  f.get.mockImplementation(() => {
    throw new ConnectError("Fixture poll unavailable", Code.Unavailable);
  });
  await act(async () => {
    await f.client.refetchQueries({ type: "active" });
  });
  expect(selectedAccountNames()).toEqual(["Select Named"]);
});

// Issue 3183: the step heading plays one 160ms Y4 entrance per user-selected
// step change and never for automatic routing, refresh or discovery.
beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete globalThis.document.documentElement.dataset.animation;
});
const stepHeading = (name: string) =>
  screen.getByRole("heading", { name, level: 3 });
function settleEntrance(node: Element) {
  const event = new Event("animationend", { bubbles: true });
  Object.defineProperty(event, "animationName", { value: "entrance-y4" });
  act(() => {
    node.dispatchEvent(event);
  });
}
it("plays one step heading entrance for each user-selected step change and none on mount", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByRole("radio", { name: "Codex" });
  const heading = stepHeading("Harness");
  expect(heading.getAttribute("data-entrance")).toBeNull();
  // Harness confirmation.
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  expect(stepHeading("Accounts")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  settleEntrance(heading);
  expect(heading.getAttribute("data-entrance")).toBeNull();
  // Back from Accounts to Harness.
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(stepHeading("Harness")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  settleEntrance(heading);
  // Next to Model, Next to Configure, then Back.
  const input = await accounts(f);
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  settleEntrance(heading);
  fireEvent.change(input, { target: { value: "typed-exact" } });
  fireEvent.keyDown(input, { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Configure", level: 3 });
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  settleEntrance(heading);
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(stepHeading("Model")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  settleEntrance(heading);
  expect(f.save).not.toHaveBeenCalled();
});

it("shows the selected harness as plain text on Accounts and returns to Harness only through Back", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  const heading = stepHeading("Accounts");
  expect(screen.queryByRole("button", { name: /Change harness/ })).toBeNull();
  expect(screen.queryByText("Change harness")).toBeNull();
  const label = screen
    .getAllByText("Codex")
    .find((node) => node.tagName === "P");
  expect(label).toBeTruthy();
  expect(label!.querySelector("button, svg, a")).toBeNull();
  // The only route back is the footer Back button, which focuses the heading.
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(stepHeading("Harness")).toBe(heading);
  expect(globalThis.document.activeElement).toBe(heading);
  // Confirming the same harness returns to Accounts.
  fireEvent.click(screen.getByRole("radio", { name: "Codex" }));
  expect(stepHeading("Accounts")).toBe(heading);
  expect(screen.queryByRole("button", { name: /Change harness/ })).toBeNull();
  expect(f.save).not.toHaveBeenCalled();
});

it("restarts the step entrance for a quick second step change without waiting for the first", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  const heading = stepHeading("Accounts");
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(stepHeading("Harness")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  // Focus moved to the heading without waiting for the motion.
  expect(globalThis.document.activeElement).toBe(heading);
});

it("does not animate model refresh, discovery, typing or refetch and preserves requests", async () => {
  const f = fixture();
  const view = render(f.view());
  const input = await accounts(f);
  const heading = stepHeading("Model");
  settleEntrance(heading);
  expect(heading.getAttribute("data-entrance")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh models from endpoint" }),
  );
  fireEvent.focus(input);
  await screen.findByRole("option", { name: /Endpoint One/ });
  fireEvent.change(input, { target: { value: "typed-exact" } });
  await f.client.invalidateQueries();
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect(stepHeading("Model")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBeNull();
  expect(f.endpoint).toHaveBeenCalledTimes(1);
  expect(input.value).toBe("typed-exact");
  expect(f.save).not.toHaveBeenCalled();
});

it("does not animate validation failure routing through fail()", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  fireEvent.change(input, { target: { value: "typed-exact" } });
  fireEvent.keyDown(input, { key: "Escape" });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Configure", level: 3 });
  const heading = stepHeading("Configure");
  settleEntrance(heading);
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Inline Worker" },
  });
  // The hidden Model control becomes invalid; Save routes back to Model.
  fireEvent.change(input, { target: { value: "x".repeat(257) } });
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  expect(stepHeading("Model")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBeNull();
  // A failed Next on the same step does not animate either.
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect(heading.getAttribute("data-entrance")).toBeNull();
  expect(f.save).not.toHaveBeenCalled();
});

it("suppresses the step entrance when Animation is Off and settles it mid-flight without replay", async () => {
  globalThis.document.documentElement.dataset.animation = "false";
  const f = fixture();
  render(f.view());
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  const heading = stepHeading("Accounts");
  expect(heading.getAttribute("data-entrance")).toBeNull();
  globalThis.document.documentElement.dataset.animation = "true";
  await act(async () => {
    await Promise.resolve();
  });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  await act(async () => {
    globalThis.document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(heading.getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    globalThis.document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(heading.getAttribute("data-entrance")).toBeNull();
});

// Stepper selection. Rows of the contract state table are named in each test:
// target unvisited, target is the current step, target before the current
// step, target after the current step with passing or failing validation,
// blocked save, inactive wizard, and visited-limit clamping.
const stepper = () =>
  within(screen.getByRole("list", { name: "Worker configuration steps" }));
const stepButton = (name: string) =>
  stepper().getByRole("button", { name }) as HTMLButtonElement;
const stepButtons = () =>
  stepper()
    .queryAllByRole("button")
    .map((button) => button.textContent!.replace(/^\d/, ""));
const currentStepName = () =>
  stepper()
    .getAllByRole("button")
    .find((button) => button.getAttribute("aria-current") === "step")
    ?.textContent?.replace(/^\d/, "");
const sideEffectFree = (f: ReturnType<typeof fixture>) => {
  expect(f.save).not.toHaveBeenCalled();
  expect(f.endpoint).not.toHaveBeenCalled();
  expect(f.known).not.toHaveBeenCalled();
};

it("renders only the opening step as a button and keeps later steps inactive", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByRole("radio", { name: "Codex" });
  expect(stepButtons()).toEqual(["Harness"]);
  expect(stepButton("Harness").getAttribute("aria-current")).toBe("step");
  expect(stepButton("Harness").type).toBe("button");
  const items = stepper().getAllByRole("listitem");
  expect(items).toHaveLength(4);
  for (const label of ["Accounts", "Model", "Configure"]) {
    const item = items.find((node) => node.textContent?.endsWith(label))!;
    // Unvisited steps expose no button, tab stop or press handler target.
    expect(item.querySelector("button, a, [tabindex]")).toBeNull();
    fireEvent.click(item);
  }
  expect(stepHeading("Harness")).toBeTruthy();
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  expect(stepButtons()).toEqual(["Harness", "Accounts"]);
  expect(currentStepName()).toBe("Accounts");
  // Clicking an unvisited item does not change the step.
  fireEvent.click(
    stepper()
      .getAllByRole("listitem")
      .find((node) => node.textContent?.endsWith("Model"))!,
  );
  expect(stepHeading("Accounts")).toBeTruthy();
  sideEffectFree(f);
});

it("moves among current and visited steps, focuses the heading and keeps drafts", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  fireEvent.change(input, { target: { value: "typed-exact" } });
  const heading = stepHeading("Model");
  settleEntrance(heading);
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model"]);
  expect(currentStepName()).toBe("Model");
  // Target before the current step, by keyboard focus then activation.
  const accountsButton = stepButton("Accounts");
  accountsButton.focus();
  expect(globalThis.document.activeElement).toBe(accountsButton);
  expect(accountsButton.tabIndex).toBe(0);
  fireEvent.click(accountsButton);
  expect(stepHeading("Accounts")).toBe(heading);
  expect(globalThis.document.activeElement).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBe("y4");
  settleEntrance(heading);
  expect(selectedAccountNames()).toHaveLength(1);
  expect(currentStepName()).toBe("Accounts");
  // Back to Harness keeps the confirmed harness and later visits.
  fireEvent.click(stepButton("Harness"));
  expect(stepHeading("Harness")).toBe(heading);
  expect(globalThis.document.activeElement).toBe(heading);
  expect(
    (
      screen.getByRole("radio", { name: "Codex" }) as HTMLInputElement
    ).getAttribute("aria-checked"),
  ).toBe("true");
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model"]);
  settleEntrance(heading);
  // Forward over a visited step re-runs validation, passes and keeps the draft.
  fireEvent.click(stepButton("Model"));
  expect(stepHeading("Model")).toBe(heading);
  expect(globalThis.document.activeElement).toBe(heading);
  expect(
    (screen.getByRole("combobox", { name: /^Model for / }) as HTMLInputElement)
      .value,
  ).toBe("typed-exact");
  settleEntrance(heading);
  // The current step refocuses the heading without replaying the entrance.
  screen.getByRole("combobox", { name: /^Model for / }).focus();
  fireEvent.click(stepButton("Model"));
  expect(globalThis.document.activeElement).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBeNull();
  expect(currentStepName()).toBe("Model");
  sideEffectFree(f);
});

it("routes a blocked forward step to the first invalid earlier stage and control", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  await configure(input);
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model", "Configure"]);
  // Invalid Model draft: Configure is blocked and Model is shown with focus.
  fireEvent.click(stepButton("Model"));
  fireEvent.change(screen.getByRole("combobox", { name: /^Model for / }), {
    target: { value: "x".repeat(257) },
  });
  fireEvent.click(stepButton("Accounts"));
  expect(stepHeading("Accounts")).toBeTruthy();
  const heading = stepHeading("Accounts");
  settleEntrance(heading);
  fireEvent.click(stepButton("Configure"));
  expect(stepHeading("Model")).toBe(heading);
  expect(heading.getAttribute("data-entrance")).toBeNull();
  expect(screen.getByRole("alert").textContent).not.toBe("");
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("combobox", { name: /^Model for / }),
  );
  expect(currentStepName()).toBe("Model");
  // Invalid Accounts draft: the first invalid stage is Accounts.
  fireEvent.click(stepButton("Accounts"));
  fireEvent.click(
    screen.getAllByRole("checkbox", { checked: true })[0] as HTMLElement,
  );
  await screen.findAllByRole("checkbox", { checked: false });
  await act(async () => {});
  fireEvent.click(stepButton("Model"));
  expect(stepHeading("Accounts")).toBe(heading);
  const control = globalThis.document.activeElement as HTMLElement;
  expect(control.getAttribute("data-wizard-field")).toBeTruthy();
  expect(screen.getByRole("alert")).toBeTruthy();
  expect(currentStepName()).toBe("Accounts");
  sideEffectFree(f);
});

it("clamps the visited limit when the harness changes", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f);
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model"]);
  fireEvent.click(stepButton("Harness"));
  // Keyboard selection of a different harness clears dependent choices.
  const codex = screen.getByRole("radio", { name: "Codex" });
  codex.focus();
  fireEvent.keyDown(codex, { key: "ArrowRight" });
  expect(stepButtons()).toEqual(["Harness"]);
  expect(screen.queryByRole("checkbox", { checked: true })).toBeNull();
  // Confirming reopens only Accounts.
  fireEvent.click(screen.getByRole("radio", { name: "Claude Code" }));
  expect(stepButtons()).toEqual(["Harness", "Accounts"]);
  expect(currentStepName()).toBe("Accounts");
  sideEffectFree(f);
});

it("keeps the visited limit when the same harness is confirmed again", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f);
  fireEvent.click(stepButton("Harness"));
  fireEvent.click(screen.getByRole("radio", { name: "Codex" }));
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model"]);
  expect(currentStepName()).toBe("Accounts");
  sideEffectFree(f);
});

it("clamps the visited limit when an account source changes or a source is added", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f);
  fireEvent.click(stepButton("Accounts"));
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model"]);
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Account source 1" }),
    "subscription:chatgpt",
  );
  expect(stepButtons()).toEqual(["Harness", "Accounts"]);
  // Reach Model again, then add a source.
  fireEvent.click(
    await screen.findByRole("checkbox", { name: /Original subscription/ }),
  );
  await waitFor(() =>
    expect(
      f.client
        .getQueryCache()
        .getAll()
        .some(
          (q) =>
            (q.state.data as { resource?: Resource } | undefined)?.resource
              ?.id === f.subscription.id &&
            q.state.status === "success" &&
            q.state.fetchStatus === "idle",
        ),
    ).toBe(true),
  );
  await act(async () => {});
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Model", level: 3 });
  expect(stepButtons()).toEqual(["Harness", "Accounts", "Model"]);
  fireEvent.click(stepButton("Accounts"));
  fireEvent.click(screen.getByRole("button", { name: /Add account source/ }));
  expect(stepButtons()).toEqual(["Harness", "Accounts"]);
  // Model entry on a subscription source reads the known catalog itself.
  expect(f.save).not.toHaveBeenCalled();
});

it("ignores stepper selection while a save is pending", async () => {
  const f = fixture();
  f.save.mockImplementationOnce(() => new Promise(() => {}));
  render(f.view());
  const input = await accounts(f);
  await configure(input);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  await act(async () => {});
  const started = f.rpc.length;
  for (const name of ["Harness", "Accounts", "Model", "Configure"]) {
    expect(stepButton(name).disabled).toBe(true);
    fireEvent.click(stepButton(name));
  }
  await act(async () => {});
  expect(f.rpc).toHaveLength(started);
  expect(stepHeading("Configure")).toBeTruthy();
  expect(f.save).toHaveBeenCalledTimes(1);
});

it("ignores stepper selection while a save is uncertain", async () => {
  const f = fixture();
  f.save.mockRejectedValueOnce(
    new ConnectError("Lost receipt", Code.Unavailable),
  );
  render(f.view());
  const input = await accounts(f);
  await configure(input);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await screen.findByRole("button", { name: "Retry the same Worker save" });
  await act(async () => {});
  const started = f.rpc.length;
  for (const name of ["Harness", "Accounts", "Model", "Configure"]) {
    expect(stepButton(name).disabled).toBe(true);
    fireEvent.click(stepButton(name));
  }
  await act(async () => {});
  expect(f.rpc).toHaveLength(started);
  expect(stepHeading("Configure")).toBeTruthy();
  expect(f.save).toHaveBeenCalledTimes(1);
});

it("disables stepper buttons while the wizard is inactive", async () => {
  const f = fixture();
  const view = render(f.view());
  await accounts(f);
  view.rerender(f.view(false));
  await act(async () => {});
  const started = f.rpc.length;
  for (const name of ["Harness", "Accounts", "Model"]) {
    expect(stepButton(name).disabled).toBe(true);
    fireEvent.click(stepButton(name));
  }
  await act(async () => {});
  expect(f.rpc).toHaveLength(started);
  expect(stepHeading("Model")).toBeTruthy();
  view.rerender(f.view());
  expect(stepButton("Harness").disabled).toBe(false);
  sideEffectFree(f);
});

it("starts an edit with only the Harness step visited", async () => {
  const f = fixture();
  const first = render(f.view());
  const model = await accounts(f);
  await configure(model);
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(1));
  const { resource } = await f.save.mock.results[0]!.value;
  first.unmount();
  render(f.view(true, resource));
  await screen.findByRole("radio", { name: "Codex" });
  expect(stepButtons()).toEqual(["Harness"]);
  expect(currentStepName()).toBe("Harness");
});

it("keeps four localized labels as buttons and plain items in Korean", async () => {
  const { i18n } = await import("./localization");
  const f = fixture();
  render(f.view());
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  try {
    await act(() => i18n.changeLanguage("ko"));
    const list = within(screen.getByRole("list", { name: "워커 구성 단계" }));
    expect(list.getAllByRole("listitem")).toHaveLength(4);
    expect(
      list.getAllByRole("button").map((b) => b.textContent!.replace(/^\d/, "")),
    ).toEqual(["하네스", "계정"]);
    expect(
      list
        .getAllByRole("listitem")
        .map((n) => n.textContent!.replace(/^\d/, "")),
    ).toEqual(["하네스", "계정", "모델", "구성"]);
    expect(
      list.getByRole("button", { name: "계정" }).getAttribute("aria-current"),
    ).toBe("step");
  } finally {
    await act(() => i18n.changeLanguage("en"));
  }
});

// Stepper read table. Selecting a step starts exactly the reads that arriving
// at that step with Back or Next starts. It starts no write, login, endpoint
// model discovery or save, and nothing for a step it jumps over.
const sideEffectFreeExceptKnown = (f: ReturnType<typeof fixture>) => {
  expect(f.save).not.toHaveBeenCalled();
  expect(f.endpoint).not.toHaveBeenCalled();
};
const ACCOUNT_READS = ["ListProviderInventory", "ListResources"];
const CONFIGURE_READS = ["ListResources"];
async function reads(f: ReturnType<typeof fixture>, action: () => void) {
  await act(async () => {});
  f.rpc.length = 0;
  action();
  await act(async () => {});
  await act(async () => {});
  return f.rpc.splice(0).sort();
}
const footer = (name: "Back" | "Next") => () =>
  fireEvent.click(screen.getByRole("button", { name }));
const select = (name: string) => () => fireEvent.click(stepButton(name));

it("starts no read when selecting Harness, the current step or an unvisited item", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f);
  expect(await reads(f, select("Model"))).toEqual([]);
  expect(await reads(f, select("Harness"))).toEqual([]);
  expect(
    await reads(f, () =>
      fireEvent.click(
        stepper()
          .getAllByRole("listitem")
          .find((node) => node.textContent?.endsWith("Configure"))!,
      ),
    ),
  ).toEqual([]);
  expect(f.save).not.toHaveBeenCalled();
});

it("starts the same Accounts reads as Back, from Model or from Configure", async () => {
  const f = fixture();
  render(f.view());
  fireEvent.change(await accounts(f), { target: { value: "typed-exact" } });
  // Model -> Accounts by the stepper, then the same arrival by Back.
  const selected = await reads(f, select("Accounts"));
  expect(selected).toEqual(ACCOUNT_READS);
  expect(await reads(f, footer("Next"))).toEqual([]);
  expect(await reads(f, footer("Back"))).toEqual(selected);
  // Configure -> Accounts jumps over Model and starts no Model read.
  expect(await reads(f, footer("Next"))).toEqual([]);
  expect(await reads(f, footer("Next"))).toEqual(CONFIGURE_READS);
  expect(await reads(f, select("Accounts"))).toEqual(selected);
  sideEffectFree(f);
});

it("starts one Accounts read pair per source group, as Back does", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f);
  fireEvent.click(stepButton("Accounts"));
  fireEvent.click(screen.getByRole("button", { name: /Add account source/ }));
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Account source 2" }),
    "subscription:chatgpt",
  );
  fireEvent.click(
    await screen.findByRole("checkbox", { name: /Original subscription/ }),
  );
  await waitFor(() =>
    expect(
      f.client
        .getQueryCache()
        .getAll()
        .some(
          (q) =>
            (q.state.data as { resource?: Resource } | undefined)?.resource
              ?.id === f.subscription.id &&
            q.state.status === "success" &&
            q.state.fetchStatus === "idle",
        ),
    ).toBe(true),
  );
  await act(async () => {});
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  await screen.findByRole("heading", { name: "Model", level: 3 });
  for (const model of screen.getAllByRole("combobox", {
    name: /^Model for /,
  }))
    fireEvent.change(model, { target: { value: "typed-exact" } });
  await act(async () => {});
  const selected = await reads(f, select("Accounts"));
  expect(selected).toEqual([...ACCOUNT_READS, ...ACCOUNT_READS].sort());
  expect(await reads(f, footer("Next"))).toEqual([]);
  expect(await reads(f, footer("Back"))).toEqual(selected);
  // The subscription group reads its catalog once, on its first Model arrival.
  expect(f.known).toHaveBeenCalledTimes(1);
  expect(f.endpoint).not.toHaveBeenCalled();
  expect(f.save).not.toHaveBeenCalled();
});

it.each(["stepper", "Back"])(
  "starts the Accounts reads once when the previous read failed, selected by %s",
  async (route) => {
    const f = fixture();
    render(f.view());
    fireEvent.change(await accounts(f), { target: { value: "typed-exact" } });
    f.list.mockImplementation(() => {
      throw new ConnectError("Fixture read unavailable", Code.Unavailable);
    });
    expect(
      await reads(f, route === "Back" ? footer("Back") : select("Accounts")),
    ).toEqual(ACCOUNT_READS);
    expect(f.save).not.toHaveBeenCalled();
  },
);

it("starts no Model read for an API source and no endpoint discovery for any source", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f);
  expect(await reads(f, select("Accounts"))).toEqual(ACCOUNT_READS);
  expect(await reads(f, select("Model"))).toEqual([]);
  expect(await reads(f, select("Harness"))).toEqual([]);
  expect(await reads(f, select("Model"))).toEqual([]);
  expect(f.rpc).not.toContain("ListEndpointModels");
  sideEffectFree(f);
});

it("reuses a fresh loaded known catalog when selecting Model", async () => {
  const f = fixture();
  render(f.view());
  await accounts(f, true);
  expect(f.known).toHaveBeenCalledTimes(1);
  expect(await reads(f, select("Accounts"))).toEqual(ACCOUNT_READS);
  expect(await reads(f, select("Model"))).toEqual([]);
  expect(await reads(f, select("Harness"))).toEqual([]);
  expect(await reads(f, select("Model"))).toEqual([]);
  expect(f.known).toHaveBeenCalledTimes(1);
  sideEffectFreeExceptKnown(f);
});

it.each(
  ["failed", "stale"].flatMap((state) =>
    ["stepper", "Back and Next"].map((route) => [state, route]),
  ),
)(
  "reads the known catalog again when it is %s, selected by %s",
  async (state, route) => {
    const f = fixture();
    if (state === "failed")
      f.known.mockImplementationOnce(() => {
        throw new ConnectError("Catalog unavailable", Code.Unavailable);
      });
    render(f.view());
    await accounts(f, true);
    await act(async () => {});
    expect(f.known).toHaveBeenCalledTimes(1);
    await reads(f, route === "stepper" ? select("Accounts") : footer("Back"));
    // The shared client keeps a loaded result fresh for a short window only.
    if (state === "stale")
      await act(async () => {
        await f.client.invalidateQueries();
      });
    expect(
      await reads(f, route === "stepper" ? select("Model") : footer("Next")),
    ).toEqual(["ListKnownSubscriptionModels"]);
    sideEffectFreeExceptKnown(f);
  },
);

it("starts the same Configure reads as Next and no save", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  await configure(input);
  expect(await reads(f, select("Accounts"))).toEqual(ACCOUNT_READS);
  // Accounts -> Configure jumps over Model and starts only Configure reads.
  const selected = await reads(f, select("Configure"));
  expect(selected).toEqual(CONFIGURE_READS);
  expect(await reads(f, footer("Back"))).toEqual([]);
  expect(await reads(f, footer("Next"))).toEqual(selected);
  expect(await reads(f, select("Configure"))).toEqual([]);
  sideEffectFree(f);
});

it("starts the reads of the first invalid stage when a forward selection is blocked", async () => {
  const f = fixture();
  render(f.view());
  const input = await accounts(f);
  await configure(input);
  fireEvent.click(stepButton("Model"));
  fireEvent.change(screen.getByRole("combobox", { name: /^Model for / }), {
    target: { value: "x".repeat(257) },
  });
  await reads(f, select("Accounts"));
  // The blocked jump shows Model, which starts no read for an API source.
  expect(await reads(f, select("Configure"))).toEqual([]);
  expect(stepHeading("Model")).toBeTruthy();
  sideEffectFree(f);
});
