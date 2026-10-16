import { create } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type Transport,
} from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { danger, expectTone, neutral } from "./test-action-tone";
import { StrictMode, useState } from "react";
import { SettingsLifetime, type SettingsOpening } from "./settings-lifetime";
import {
  SystemService,
  SystemCapability,
  configurationSchemaVersion,
  AccountService,
  ApiAuthentication,
  ApiProtocol,
  ConfigurationService,
  EntityKind,
  ErrorDetailSchema,
  ProviderApiFormatSchema,
  ProviderInventoryCapability,
  ProviderInventoryEntrySchema,
  ProviderPresetId,
  ProviderService,
  ResourceSchema,
  ResourceService,
  WorkerService,
  newRequestId,
  type ListResourcesRequest,
  type ProviderInventoryEntry,
  type Resource,
} from "@delinoio/delidev-api-client";
import { Settings, ConfigurationEditor } from "./settings";
import {
  fakeDestinationWindow,
  primaryClick,
} from "./destination-window-test-support";
import { Surface } from "./surface";
import { SettingsCategory } from "./settings-category";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { copy, i18n, SupportedLanguage } from "./localization";
import { RepositoryRow } from "./repository-list";
import { AccountConnection } from "./account-connection";
import { ConfigurationDeletion, RoutingPreview } from "./configuration-actions";
import { MutationIntents } from "./mutation";
import { encode, type Document } from "./documents";
import { chooseScrollOption, scrollChoiceValue } from "./test-scroll-picker";
import { NotificationProvider } from "./toast-notifications";

function resource(kind: EntityKind, value: Document, revision = 1n) {
  if (
    kind === EntityKind.AGENT &&
    !Object.hasOwn(value, "model_id") &&
    !Object.hasOwn(value, "routes")
  )
    value = {
      ...value,
      routes: [
        {
          model: {
            subscription_service: "chatgpt",
            native_id: "fixture-native",
          },
          accounts: [{ id: newRequestId(), weight: 1 }],
        },
      ],
    };
  return create(ResourceSchema, {
    id: newRequestId(),
    kind,
    schemaVersion: configurationSchemaVersion(kind, value),
    revision,
    documentJson: encode(value),
  });
}
function fixture(
  resources: Resource[],
  options: {
    readResource?: (
      id: string,
    ) => Promise<{ resource?: Resource }> | { resource?: Resource };
    providerEntries?: ProviderInventoryEntry[];
    presets?: unknown[];
    providerInventoryError?: ConnectError;
    systemStatusError?: ConnectError;
    systemCapabilities?: SystemCapability[];
    readResources?: (
      kind: EntityKind,
      pageToken: string,
    ) =>
      | { resources: Resource[]; nextPageToken?: string }
      | Promise<{ resources: Resource[]; nextPageToken?: string }>;
    readProviderInventory?: (
      pageToken: string,
      request: { query: string; enabledOnly: boolean; pageSize: number },
    ) =>
      | {
          entries: ProviderInventoryEntry[];
          capabilities: ProviderInventoryCapability[];
          nextPageToken?: string;
        }
      | Promise<{
          entries: ProviderInventoryEntry[];
          capabilities: ProviderInventoryCapability[];
          nextPageToken?: string;
        }>;
    doctor?: () => { reportJson?: Uint8Array };
    readModelSearch?: (pageToken: string) => {
      models: Resource[];
      providers: Resource[];
      nextPageToken?: string;
    };
  } = {},
) {
  const save = vi.fn(
    async (
      _request: unknown,
    ): Promise<{ resource?: Resource; job?: Resource }> => ({
      resource: resources[0],
    }),
  );
  const remove = vi.fn(async (_request: unknown) => ({}));
  const preview = vi.fn(async (_request: unknown) => ({
    routeJson: encode({
      policy: "remaining-quota",
      selected: "",
      candidates: [],
    }),
  }));
  const inspect = vi.fn(async (_request: unknown) => ({
    job: resources.find((row) => row.kind === EntityKind.JOB),
  }));
  const connect = vi.fn(async (_request: unknown) => ({
    account: resources.find((row) => row.kind === EntityKind.ACCOUNT),
  }));
  const disconnect = vi.fn(async (_request: unknown) => ({
    account: resources.find((row) => row.kind === EntityKind.ACCOUNT),
  }));
  const list = vi.fn(
    (request: ListResourcesRequest) =>
      options.readResources?.(
        request.filter?.kind ?? EntityKind.UNSPECIFIED,
        request.filter?.pageToken ?? "",
      ) ?? {
        resources: resources.filter((row) => row.kind === request.filter?.kind),
      },
  );
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getDoctor:
        options.doctor ??
        vi.fn(() => {
          throw new Error("Doctor is not requested by Settings");
        }),
      getStatus: () => {
        if (options.systemStatusError) throw options.systemStatusError;
        return {
          protocolVersion: 2,
          capabilities: options.systemCapabilities ?? [
            SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1,
            SystemCapability.REMOTE_REPOSITORIES_V1,
            SystemCapability.INLINE_WORKER_MODELS_V1,
          ],
        };
      },
    });
    router.service(ConfigurationService, {
      saveConfiguration: save,
      deleteConfiguration: remove,
      previewRouting: preview,
    });
    router.service(WorkerService, { inspectRepository: inspect });
    router.service(ResourceService, {
      listResources: list,
      getResource: (request) =>
        options.readResource?.(request.id) ?? {
          resource: resources.find((row) => row.id === request.id),
        },
    });
    router.service(AccountService, {
      getAccountStatus: (request) => ({
        account: resources.find((row) => row.id === request.id),
      }),
      connectAccount: connect,
      disconnectAccount: disconnect,
    });
    router.service(ProviderService, {
      listProviderPresets: () => ({
        presetsJson: encode(
          options.presets ?? [
            {
              id: "ollama",
              provider: {
                name: "Local provider",
                endpoint: "http://127.0.0.1:11434/v1",
                protocol: "openai-chat",
                authentication: "keyless",
                discovery: true,
              },
              key_guidance: "Run your local model server first.",
              compatibility: "Requires a compatible model.",
            },
          ],
        ),
      }),
      listProviderInventory: (request) => {
        if (options.providerInventoryError)
          throw options.providerInventoryError;
        return (
          options.readProviderInventory?.(request.pageToken, request) ?? {
            entries: options.providerEntries ?? [
              {
                presetId: ProviderPresetId.OLLAMA,
                displayName: "Local provider",
                enabled: false,
                totalAccounts: 0n,
                connectedAccounts: 0n,
                accountCountsAvailable: true,
              },
            ],
            capabilities: [
              ProviderInventoryCapability.PROVIDER_ACTIVATION,
              ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
              ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1,
              ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
              ...(options.providerEntries
                ? [ProviderInventoryCapability.ACCOUNT_TYPE_FILTER]
                : []),
            ],
          }
        );
      },
      searchModels: (request) =>
        options.readModelSearch?.(request.pageToken) ?? {
          models: [],
          providers: [],
        },
    });
  });
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false, gcTime: 0 },
    },
  });
  const view = (children: React.ReactNode) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>{children}</MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return {
    resources,
    save,
    remove,
    preview,
    inspect,
    connect,
    disconnect,
    client,
    transport,
    list,
    view,
  };
}
function input(value: unknown) {
  return value as {
    mutation: { requestId: string; expectedRevision: bigint };
    documentJson: Uint8Array;
  };
}

it("shows a configuration toast only for an immediate saved resource", async () => {
  const repository = resource(EntityKind.REPOSITORY, { name: "Repository" });
  const project = resource(EntityKind.PROJECT, {
    name: "Project",
    repositories: [repository.id],
    primary_repository: repository.id,
    agents: { configured: false, ids: [] },
    accounts: { configured: false, ids: [] },
  });
  const value = fixture([project, repository]);
  const saved = vi.fn();
  render(
    <NotificationProvider>
      {value.view(
        <ConfigurationEditor
          kind={EntityKind.PROJECT}
          initial={project}
          active
          saved={saved}
          cancel={() => {}}
        />,
      )}
    </NotificationProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  expect(await screen.findByText("Project saved.")).toBeTruthy();
  expect(saved).toHaveBeenCalledTimes(1);
});

it.each(["job", "unknown", "failed", "uncertain"])(
  "does not show configuration success for a %s outcome",
  async (outcome) => {
    const repository = resource(EntityKind.REPOSITORY, { name: "Repository" });
    const project = resource(EntityKind.PROJECT, {
      name: "Project",
      repositories: [repository.id],
      primary_repository: repository.id,
      agents: { configured: false, ids: [] },
      accounts: { configured: false, ids: [] },
    });
    const job = resource(EntityKind.JOB, {
      type: "save-project",
      state: "queued",
    });
    const value = fixture([project, job, repository]);
    const saved = vi.fn();
    if (outcome === "failed" || outcome === "uncertain")
      value.save.mockRejectedValueOnce(
        new ConnectError(
          "Fixture save failure",
          outcome === "failed" ? Code.InvalidArgument : Code.Unavailable,
        ),
      );
    else
      value.save.mockResolvedValueOnce(
        outcome === "job" ? { job, resource: project } : {},
      );
    render(
      <NotificationProvider>
        {value.view(
          <ConfigurationEditor
            kind={EntityKind.PROJECT}
            initial={project}
            active
            saved={saved}
            cancel={() => {}}
          />,
        )}
      </NotificationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    if (outcome === "job")
      await screen.findByText(
        "Accepted by the server. Waiting for the selected Worker to finish.",
      );
    else await screen.findByRole("alert");
    expect(screen.queryByText("Project saved.")).toBeNull();
    expect(saved).not.toHaveBeenCalled();
  },
);

it("renders the Agent-only empty inventory after the first read succeeds", async () => {
  let resolve!: (result: { resources: Resource[] }) => void;
  const pending = new Promise<{ resources: Resource[] }>((done) => {
    resolve = done;
  });
  const value = fixture([], {
    readResources: (kind) =>
      kind === EntityKind.AGENT ? pending : { resources: [] },
  });
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  expect(screen.getByRole("status").textContent).toBe("Loading agent workers…");
  expect(
    screen.queryByRole("region", { name: "No agent workers yet" }),
  ).toBeNull();
  resolve({ resources: [] });
  const panel = await screen.findByRole("region", {
    name: "No agent workers yet",
  });
  expect(
    screen.getAllByRole("heading", { level: 1, name: "Agent Workers" }),
  ).toHaveLength(1);
  expect(
    screen.getByText("Reusable configurations for your agents."),
  ).toBeTruthy();
  expect(
    screen.getByText("Saved on the selected server.", {
      selector:
        ".settings-agent-column > .settings-category-heading .settings-scope",
    }),
  ).toBeTruthy();
  expect(
    within(panel).getByRole("heading", { name: "No agent workers yet" }),
  ).toBeTruthy();
  expect(
    within(panel).getByText(
      "Define a harness, model, accounts, and instructions, then reuse them in new sessions.",
    ),
  ).toBeTruthy();
  expect(panel.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  expect(within(panel).queryByRole("button")).toBeNull();
  expect(
    screen.getAllByRole("button", { name: "New Agent Worker" }),
  ).toHaveLength(1);
  expect(
    screen.queryByRole("navigation", { name: "Settings pages" }),
  ).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.preview).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(
    screen
      .getByRole("heading", { level: 1, name: "Instructions" })
      .closest(".settings-agent-column"),
  ).toBeNull();
  expect(
    screen.queryByText("Reusable configurations for your agents."),
  ).toBeNull();
});

it.each([Code.PermissionDenied, Code.Unavailable])(
  "keeps an initial Agent read failure %s distinct from empty inventory",
  async (code) => {
    const value = fixture([], {
      readResources: (kind) => {
        if (kind === EntityKind.AGENT)
          throw new ConnectError("Synthetic read failure", code);
        return { resources: [] };
      },
    });
    render(value.view(<Settings />));
    fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(
      screen.queryByRole("region", { name: "No agent workers yet" }),
    ).toBeNull();
    expect(
      screen.queryByText(
        "Refresh failed. Showing the last successfully loaded results.",
      ),
    ).toBeNull();
    expect(screen.queryByText("No agent workers on this page.")).toBeNull();
  },
);

it.each([true, false])(
  "preserves Agent opaque pages when the first page is empty: %s",
  async (firstEmpty) => {
    const agent = resource(EntityKind.AGENT, { name: "Paged Agent" });
    const value = fixture([agent], {
      readResources: (kind, token) =>
        kind === EntityKind.AGENT
          ? {
              resources: token || firstEmpty ? [] : [agent],
              nextPageToken: token ? "" : "agent-page-2",
            }
          : { resources: [] },
    });
    render(value.view(<Settings />));
    fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
    if (firstEmpty) await screen.findByText("No agent workers on this page.");
    else await screen.findByRole("heading", { name: "Paged Agent" });
    expect(
      screen.queryByRole("region", { name: "No agent workers yet" }),
    ).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: "Load more Settings pages" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Load more Settings pages" }),
      ).toBeNull(),
    );
    if (!firstEmpty)
      expect(screen.getByRole("heading", { name: "Paged Agent" })).toBeTruthy();
    else
      expect(
        screen.getByRole("region", { name: "No agent workers yet" }),
      ).toBeTruthy();
    expect(
      value.list.mock.calls
        .filter(([request]) => request.filter?.kind === EntityKind.AGENT)
        .map(([request]) => request.filter),
    ).toEqual([
      expect.objectContaining({
        kind: EntityKind.AGENT,
        pageSize: 50,
        pageToken: "",
      }),
      expect.objectContaining({
        kind: EntityKind.AGENT,
        pageSize: 50,
        pageToken: "agent-page-2",
      }),
    ]);
    expect(value.save).not.toHaveBeenCalled();
  },
);

it.each([true, false])(
  "retains cached Agent results through a failed refresh, empty: %s",
  async (empty) => {
    const agent = resource(EntityKind.AGENT, { name: "Cached Agent" });
    let reject!: (error: ConnectError) => void,
      refresh = false;
    const pending = new Promise<{ resources: Resource[] }>((_resolve, fail) => {
      reject = fail;
    });
    const value = fixture([agent], {
      readResources: (kind) =>
        kind === EntityKind.AGENT
          ? refresh
            ? pending
            : { resources: empty ? [] : [agent] }
          : { resources: [] },
    });
    render(value.view(<Settings />));
    fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
    if (empty)
      await screen.findByRole("region", { name: "No agent workers yet" });
    else await screen.findByRole("heading", { name: "Cached Agent" });
    refresh = true;
    fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
    await waitFor(() =>
      expect(
        value.list.mock.calls.filter(
          ([request]) => request.filter?.kind === EntityKind.AGENT,
        ),
      ).toHaveLength(2),
    );
    const cachedRow = empty
      ? undefined
      : screen.getByRole("heading", { name: "Cached Agent" });
    expect(screen.queryByText("Loading agent workers…")).toBeNull();
    reject(new ConnectError("Refresh unavailable", Code.Unavailable));
    await screen.findByRole("alert");
    expect(screen.getByRole("status").textContent).toBe(
      "Refresh failed. Showing the last successfully loaded results.",
    );
    expect(
      screen.queryByRole("region", { name: "No agent workers yet" }),
    ).toBeNull();
    if (cachedRow)
      expect(screen.getByRole("heading", { name: "Cached Agent" })).toBe(
        cachedRow,
      );
    expect(value.save).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
  },
);

it("keeps Agent row content inert and actions scoped to exact supported configurations", async () => {
  const name = `<b>${"LongAgent".repeat(40)}</b>`;
  const agent = resource(EntityKind.AGENT, {
    name,
    harness: "codex",
    health: "saved",
  });
  const future = create(ResourceSchema, {
    ...resource(EntityKind.AGENT, { name: "Future Agent" }),
    schemaVersion: 2,
  });
  const value = fixture([agent, future]);
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  const heading = await screen.findByRole("heading", { name });
  const panel = heading.closest(".settings-agent-list")!;
  expect(panel.querySelectorAll(".settings-agent-row")).toHaveLength(2);
  const row = within(heading.closest("article")!);
  expect(row.queryByText(agent.id)).toBeNull();
  expect(row.getByText("Codex")).toBeTruthy();
  expect(row.getByText("Status: saved")).toBeTruthy();
  expect(heading.querySelector("b")).toBeNull();
  expect(
    row.getAllByRole("button").map((button) => button.textContent),
  ).toEqual(["Edit", "Preview routing", "Delete"]);
  const futureRow = within(
    screen.getByRole("heading", { name: "Future Agent" }).closest("article")!,
  );
  expect(futureRow.queryByText(future.id)).toBeNull();
  expect(futureRow.queryByText(/Harness:|Status:/)).toBeNull();
  for (const button of futureRow.getAllByRole("button")) {
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button);
  }
  expect(value.save).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.preview).not.toHaveBeenCalled();
  fireEvent.click(
    row.getByRole("button", { name: `Preview routing for ${name}` }),
  );
  await waitFor(() =>
    expect(value.preview).toHaveBeenCalledWith(
      expect.objectContaining({ agentId: agent.id }),
      expect.anything(),
    ),
  );
  expect(screen.getByRole("dialog").getAttribute("data-size")).toBe("form");
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: /^Close / }),
  );
  fireEvent.click(screen.getByRole("button", { name: `Edit ${name}` }));
  expect(screen.getByRole("dialog").getAttribute("data-size")).toBe("wide");
  expect(await screen.findByRole("heading", { name: "Harness" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  expect(screen.queryByRole("button", { name: "New Agent Worker" })).toBeNull();
  expect(
    (screen.getByRole("button", { name: "Projects" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "Close Edit Agent Worker" }),
  );
  fireEvent.click(screen.getByRole("button", { name: `Delete ${name}` }));
  expect(
    screen.getByText(
      "Schedules using this configuration will be disabled for future runs. Already accepted sessions are retained.",
    ),
  ).toBeTruthy();
  expect(value.remove).not.toHaveBeenCalled();
});

it.each([
  { field: "name", projected: "a".repeat(256), valid: true },
  { field: "name", projected: "a".repeat(257), valid: false },
  { field: "alias", projected: "😀".repeat(64), valid: true },
  { field: "alias", projected: "😀".repeat(65), valid: false },
  { field: "name", projected: "a".repeat(512 << 10), valid: false },
  { field: "alias", projected: "a".repeat(512 << 10), valid: false },
] as const)(
  "bounds an unsupported Agent's $field display text before duplicating labels, valid: $valid",
  async ({ field, projected, valid }) => {
    const future = create(ResourceSchema, {
      ...resource(EntityKind.AGENT, { [field]: projected }),
      schemaVersion: 2,
    });
    const value = fixture([future]);
    render(value.view(<Settings />));
    fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
    const name = valid ? projected : "Agent unavailable · 1";
    const heading = await screen.findByRole("heading", { name });
    expect(heading.textContent).toBe(name);
    const row = within(heading.closest("article")!);
    expect(row.queryByText(future.id)).toBeNull();
    for (const label of [
      `Edit ${name}`,
      `Preview routing for ${name}`,
      `Delete ${name}`,
    ]) {
      expect(
        (row.getByRole("button", { name: label }) as HTMLButtonElement)
          .disabled,
      ).toBe(true);
    }
    expect(value.save).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
    expect(value.preview).not.toHaveBeenCalled();
  },
);

it("keeps the original Agent deletion revision and retry request within its opening", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Retained Agent" }, 7n),
    value = fixture([agent]);
  value.remove.mockRejectedValueOnce(
    new ConnectError("Acknowledgment lost", Code.Unavailable),
  );
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: `Delete Retained Agent`,
    }),
  );
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm configuration deletion" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same deletion" }),
  );
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[1][0]).toEqual(value.remove.mock.calls[0][0]);
  expect(value.remove.mock.calls[0][0]).toMatchObject({
    kind: EntityKind.AGENT,
    mutation: { id: agent.id, expectedRevision: 7n },
  });
});

it("uses service identity without Provider reads when editing native account preferences", async () => {
  const account = resource(EntityKind.ACCOUNT, {
    alias: "Native account",
    type: "subscription",
    subscription_service: "chatgpt",
  });
  const value = fixture([account]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.ACCOUNT}
        initial={account}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  expect(screen.getByText("Subscription service: ChatGPT")).toBeTruthy();
  expect(screen.queryByLabelText("Provider")).toBeNull();
  expect(
    screen.queryByRole("navigation", {
      name: "Native subscription provider choices",
    }),
  ).toBeNull();
  expect(
    value.list.mock.calls.every(
      ([request]) => request.filter?.kind !== EntityKind.PROVIDER,
    ),
  ).toBe(true);
});

it("keeps account continuation cursors out of provider inventory requests", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "API provider",
    enabled: true,
    protocol: "openai-responses",
    authentication: "keyless",
    endpoint: "https://fixture.test/v1",
  });
  const account = (alias: string) =>
    resource(EntityKind.ACCOUNT, {
      alias,
      provider_id: provider.id,
      type: "api",
      enabled: true,
      health: "unverified",
      connection: { id: newRequestId(), authentication: "keyless" },
    });
  const first = account("First account"),
    second = account("Second account");
  const providerEntry = create(ProviderInventoryEntrySchema, {
    providerId: provider.id,
    displayName: "API provider",
    enabled: true,
    provider,
  });
  const inventoryTokens: string[] = [],
    accountTokens: string[] = [];
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
    ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
    ProviderInventoryCapability.COMPATIBLE_ACCOUNT_API_PROTOCOL_FILTER_V1,
  ];
  const value = fixture([provider, first, second], {
    providerEntries: [providerEntry],
    readProviderInventory: (pageToken) => {
      inventoryTokens.push(pageToken);
      if (pageToken)
        throw new ConnectError(
          "Provider inventory received another query's cursor",
          Code.InvalidArgument,
        );
      return { entries: [providerEntry], capabilities };
    },
    readResources: (kind, pageToken) => {
      if (kind !== EntityKind.ACCOUNT) return { resources: [] };
      accountTokens.push(pageToken);
      return pageToken
        ? { resources: [second] }
        : { resources: [first], nextPageToken: "account-page-2" };
    },
  });
  render(
    value.view(<AgentWorkerWizard active saved={() => {}} cancel={() => {}} />),
  );
  fireEvent.click(await screen.findByRole("radio", { name: "Codex" }));
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Account source 1" }),
    `api:${provider.id}`,
  );
  await screen.findByRole("checkbox", { name: /First account/ });
  fireEvent.click(screen.getByRole("button", { name: /Load more.*account/i }));
  await screen.findByRole("checkbox", { name: /Second account/ });
  expect(accountTokens).toEqual(["", "account-page-2"]);
  expect(inventoryTokens.every((pageToken) => pageToken === "")).toBe(true);
  expect(
    value.list.mock.calls.every(
      ([request]) => request.filter?.kind !== EntityKind.MODEL,
    ),
  ).toBe(true);
  expect(value.save).not.toHaveBeenCalled();
});

it("shows the complete grouped navigation once and keeps its selected category in sync", async () => {
  const value = fixture([]);
  render(value.view(<Settings visible />));
  const navigation = screen.getByRole("navigation", {
    name: "Settings categories",
  });
  const labels = [
    "AI Subscription",
    "AI API Keys",
    "API Providers",
    "Agent Workers",
    "Instructions",
    "Project defaults",
    "Projects",
    "Repositories",
    "Git Profiles",
    "Runner Devices",
    "Paired devices",
    "Appearance",
    "Browser",
    "Keyboard shortcuts",
    "Server preferences",
    "Connections",
    "Notifications",
    "Import / Export",
    "Backups",
    "App information",
  ];
  const values = [
    "subscription-accounts",
    "api-accounts",
    "providers",
    "agent-workers",
    "instructions",
    "project-defaults",
    "projects",
    "repositories",
    "integrations",
    "execution-workers",
    "paired-devices",
    "appearance",
    "browser",
    "keyboard-shortcuts",
    "server-preferences",
    "diagnostics",
    "notifications",
    "transfer",
    "backups",
    "app-information",
  ];
  expect(
    Array.from(
      navigation.querySelectorAll(".settings-nav-group h2"),
      (heading) => heading.textContent,
    ),
  ).toEqual(["AI", "Coding", "Device management", "System"]);
  expect(
    within(navigation)
      .getAllByRole("button")
      .map((button) => button.textContent?.trim().replace(/\s+/g, " ")),
  ).toEqual(labels);
  const buttons = within(navigation).getAllByRole("button");
  expect(
    buttons.map((button) => button.getAttribute("data-settings-category")),
  ).toEqual(values);
  expect(
    screen.queryByRole("combobox", { name: "Settings category" }),
  ).toBeNull();
  expect(screen.queryByRole("dialog", { name: "Settings" })).toBeNull();
  for (const label of labels) {
    const button = within(navigation).getByRole("button", { name: label });
    fireEvent.click(button);
    expect(button.getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("heading", { level: 1, name: label })).toBeTruthy();
  }
  fireEvent.click(
    within(navigation).getByRole("button", { name: "AI Subscription" }),
  );
  expect(
    screen.getByRole("heading", { level: 1, name: "AI Subscription" }),
  ).toBeTruthy();
});

it("keeps API provider accounts optional when none are connected", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "OpenAI",
    endpoint: "https://api.openai.com/v1",
    protocol: "openai-responses",
    authentication: "bearer",
    discovery: true,
    enabled: true,
    preset_id: "openai",
  });
  const value = fixture([], {
    providerEntries: [
      create(ProviderInventoryEntrySchema, {
        presetId: ProviderPresetId.OPENAI,
        providerId: provider.id,
        displayName: "OpenAI",
        enabled: true,
        totalAccounts: 0n,
        connectedAccounts: 0n,
        provider,
        accountCountsAvailable: true,
      }),
    ],
  });
  render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  expect(
    await screen.findByText(/Entries: 0 connected · 0 total/),
  ).toBeTruthy();
  expect(screen.queryByText("Account required")).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(
    await screen.findByRole("heading", { name: "Connect your entry" }),
  ).toBeTruthy();
});

it("keeps provider-row entry fields disabled when the server lacks account-type filtering", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "OpenAI",
    endpoint: "https://api.openai.com/v1",
    protocol: "openai-responses",
    authentication: "bearer",
    enabled: true,
    preset_id: "openai",
  });
  const entry = create(ProviderInventoryEntrySchema, {
    presetId: ProviderPresetId.OPENAI,
    providerId: provider.id,
    displayName: "OpenAI",
    enabled: true,
    provider,
    accountCountsAvailable: true,
  });
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
  ];
  const value = fixture([provider], {
    readProviderInventory: () => ({ entries: [entry], capabilities }),
  });
  render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Add AI API key" }),
  );
  await screen.findByRole("heading", { name: "Connect your entry" });
  expect(screen.getByLabelText("Entry name").matches(":disabled")).toBe(true);
  expect(screen.getByLabelText("API key").matches(":disabled")).toBe(true);
  expect(
    (
      screen.getByRole("button", {
        name: "Add and connect",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText(/server no longer reports/)).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("still reports a real provider inventory read failure", async () => {
  const value = fixture([], {
    providerInventoryError: new ConnectError(
      "Inventory unavailable",
      Code.Unavailable,
    ),
  });
  render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  expect(await screen.findByRole("alert")).toBeTruthy();
});

it("retains the exact first-activation retry after inventory reveals the saved preset", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "Local provider",
    endpoint: "http://127.0.0.1:11434/v1",
    protocol: "openai-chat",
    authentication: "keyless",
    discovery: true,
    enabled: true,
    preset_id: "ollama",
  });
  let entry = create(ProviderInventoryEntrySchema, {
    presetId: ProviderPresetId.OLLAMA,
    displayName: "Local provider",
    enabled: false,
    accountCountsAvailable: true,
  });
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
  ];
  const value = fixture([provider], {
    readProviderInventory: () => ({ entries: [entry], capabilities }),
  });
  value.save.mockImplementationOnce(async () => {
    entry = create(ProviderInventoryEntrySchema, {
      ...entry,
      providerId: provider.id,
      provider,
      enabled: true,
    });
    throw new ConnectError("Activation acknowledgment lost", Code.Unavailable);
  });
  render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const originalSwitch = await screen.findByRole("switch", {
    name: "Turn on Local provider",
  });
  fireEvent.click(originalSwitch);
  const savedSwitch = await screen.findByRole("switch", {
    name: "Turn off Local provider",
  });
  expect(savedSwitch).toBe(originalSwitch);
  expect((savedSwitch as HTMLButtonElement).disabled).toBe(true);
  expect(value.save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same change" }),
  );
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[1][0]).toEqual(value.save.mock.calls[0][0]);
  expect(input(value.save.mock.calls[1][0]).mutation).toMatchObject({
    id: "",
    expectedRevision: 0n,
  });
  await waitFor(() =>
    expect(
      (
        screen.getByRole("switch", {
          name: "Turn off Local provider",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  expect(
    screen.queryByRole("button", { name: "Retry the same change" }),
  ).toBeNull();
});

it("uses server-owned preset key guidance and inert documentation in the API account wizard", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "OpenAI",
    endpoint: "https://api.openai.com/v1",
    protocol: "openai-responses",
    authentication: "bearer",
    discovery: true,
    enabled: true,
    preset_id: "openai",
  });
  const value = fixture([], {
    providerEntries: [
      create(ProviderInventoryEntrySchema, {
        presetId: ProviderPresetId.OPENAI,
        providerId: provider.id,
        displayName: "OpenAI",
        enabled: true,
        totalAccounts: 0n,
        connectedAccounts: 0n,
        provider,
        accountCountsAvailable: true,
      }),
    ],
    presets: [
      {
        id: "openai",
        provider: {
          name: "OpenAI",
          endpoint: "https://api.openai.com/v1",
          protocol: "openai-responses",
          authentication: "bearer",
          discovery: true,
        },
        key_guidance: "Create a project key for the selected workspace.",
        documentation: "https://developers.openai.com/api/reference/overview",
      },
    ],
  });
  render(value.view(<Settings visible />));
  fireEvent.click(await screen.findByRole("button", { name: "AI API Keys" }));
  const add = await screen.findByRole("button", { name: "Add AI API key" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  fireEvent.click(
    await screen.findByRole("button", { name: "OpenAI API key" }),
  );
  fireEvent.click(screen.getByText("Where to get an API key"));
  expect(
    screen.getByText("Create a project key for the selected workspace."),
  ).toBeTruthy();
  expect(
    screen.getByText("https://developers.openai.com/api/reference/overview"),
  ).toBeTruthy();
  expect(
    screen.queryByRole("link", {
      name: "https://developers.openai.com/api/reference/overview",
    }),
  ).toBeNull();
});

it.each(["close", "category change"])(
  "discards account filters, later pages, wizard input and deletion confirmations on %s",
  async (departure) => {
    const provider = resource(EntityKind.PROVIDER, {
      name: "OpenAI",
      endpoint: "https://api.openai.com/v1",
      protocol: "openai-responses",
      authentication: "bearer",
      discovery: true,
      enabled: true,
    });
    const instructions = resource(EntityKind.TEMPLATE, {
      name: "Saved instructions",
      contents: "Server contents",
    });
    const entry = create(ProviderInventoryEntrySchema, {
      providerId: provider.id,
      displayName: "OpenAI",
      enabled: true,
      provider,
      accountCountsAvailable: true,
    });
    const tokens: string[] = [];
    const value = fixture([provider, instructions], {
      providerEntries: [entry],
      readResources: (kind, token) => {
        if (kind === EntityKind.ACCOUNT) {
          tokens.push(token);
          return {
            resources: [],
            nextPageToken: token ? "" : "account-page-2",
          };
        }
        return {
          resources: kind === EntityKind.TEMPLATE ? [instructions] : [],
        };
      },
    });
    const view = render(value.view(<Settings />));
    const leave = () => {
      if (departure === "category change")
        fireEvent.click(screen.getByRole("button", { name: "Projects" }));
      else {
        view.rerender(value.view(<Settings visible={false} />));
        view.rerender(value.view(<Settings />));
      }
    };
    fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
    expect(
      screen.queryByRole("searchbox", { name: "Search providers" }),
    ).toBeNull();
    const next = await screen.findByRole("button", {
      name: "Load more Entry pages",
    });
    await waitFor(() =>
      expect((next as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(next);
    await waitFor(() => expect(tokens).toContain("account-page-2"));
    fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "OpenAI API key" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Entry name" }), {
      target: { value: "Abandoned account" },
    });
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "fixture-transient-key" },
    });
    leave();
    fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
    expect(
      screen.queryByRole("searchbox", { name: "Search providers" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Clear provider filter" }),
    ).toBeNull();
    await waitFor(() => expect(tokens.at(-1)).toBe(""));
    expect(screen.queryByRole("textbox", { name: "Entry name" })).toBeNull();
    expect(screen.queryByLabelText("API key")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
    fireEvent.click(
      await screen.findByRole("button", {
        name: `Delete Saved instructions`,
      }),
    );
    expect(
      screen.getByRole("button", { name: "Confirm configuration deletion" }),
    ).toBeTruthy();
    leave();
    fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
    expect(
      await screen.findByRole("button", {
        name: `Delete Saved instructions`,
      }),
    ).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Confirm configuration deletion" }),
    ).toBeNull();
    expect(value.save).not.toHaveBeenCalled();
    expect(value.connect).not.toHaveBeenCalled();
    expect(value.remove).not.toHaveBeenCalled();
  },
);

it("keeps exact retries within an opening and discards its provider draft on close", async () => {
  const value = fixture([]);
  value.save.mockRejectedValueOnce(
    new ConnectError("acknowledgement lost", Code.Unavailable),
  );
  const view = render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const create = await screen.findByRole("button", { name: "Custom provider" });
  await waitFor(() =>
    expect((create as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(create);
  expect(
    screen.queryByRole("checkbox", {
      name: "Discover models automatically for connected entries",
    }),
  ).toBeNull();
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
    target: { value: "My local provider" },
  });
  await waitFor(() =>
    expect(
      screen
        .getByRole("checkbox", { name: "OpenAI Responses" })
        .matches(":disabled"),
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "OpenAI Responses" }));
  fireEvent.change(screen.getByRole("textbox", { name: "API base URL" }), {
    target: { value: "http://127.0.0.1:11434/v1" },
  });
  expect(
    (screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value,
  ).toBe("My local provider");
  fireEvent.click(screen.getByRole("button", { name: "Save Provider" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same configuration" }),
  );
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(0n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({
    name: "My local provider",
    endpoint: "http://127.0.0.1:11434/v1",
    protocol: "openai-responses",
    authentication: "bearer",
    discovery: true,
    enabled: true,
    api_formats: [
      {
        protocol: "openai-responses",
        endpoint: "http://127.0.0.1:11434/v1",
        authentication: "bearer",
      },
    ],
  });
  view.rerender(value.view(<Settings visible={false} />));
  view.rerender(value.view(<Settings visible />));
  expect(
    screen
      .getByRole("button", { name: "AI Subscription" })
      .getAttribute("aria-current"),
  ).toBe("page");
  expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Retry the same configuration" }),
  ).toBeNull();
  expect(value.save).toHaveBeenCalledTimes(2);
});

it("preserves server-owned account observations during a preference edit", async () => {
  const provider = resource(EntityKind.PROVIDER, { name: "Provider" });
  const observed = {
    alias: "Original",
    provider_id: provider.id,
    type: "api",
    enabled: true,
    exclude_automatic: false,
    recovery_notifications: true,
    health: "ready",
    connection: {
      id: newRequestId(),
      authentication: "bearer",
      connected_at: "2026-09-25T00:00:00Z",
    },
    quota: [{ id: "window", remaining: 0 }],
    confirmed_exhausted: true,
    validation: { state: "observed" },
    catalog: { state: "stale" },
  };
  const account = resource(EntityKind.ACCOUNT, observed, 7n);
  const value = fixture([account, provider]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.ACCOUNT}
        initial={account}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Entry name" }), {
    target: { value: "Renamed" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Save AI API key entry" }),
  );
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(7n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({
    ...observed,
    alias: "Renamed",
  });
});

it("blocks stale settings writes without erasing the staged instructions", async () => {
  const initial = resource(
    EntityKind.TEMPLATE,
    { name: "Instructions", contents: "Original instructions" },
    3n,
  );
  const value = fixture([create(ResourceSchema, { ...initial, revision: 4n })]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.TEMPLATE}
        initial={initial}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Instructions" }), {
    target: { value: "My staged instructions" },
  });
  await screen.findByText(/This entry changed elsewhere/);
  fireEvent.submit(
    (
      screen.getByRole("button", {
        name: "Save Instructions",
      }) as HTMLButtonElement
    ).form!,
  );
  expect(value.save).not.toHaveBeenCalled();
  expect(
    (
      screen.getByRole("textbox", {
        name: "Instructions",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("My staged instructions");
});

it("retains a secret only for its exact uncertain connection and excludes it from read cache keys", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "API provider",
    authentication: "bearer",
    protocol: "openai-responses",
    endpoint: "https://api.example.test/v1",
  });
  const account = resource(
    EntityKind.ACCOUNT,
    {
      alias: "API account",
      provider_id: provider.id,
      type: "api",
      health: "disconnected",
    },
    5n,
  );
  const value = fixture([account, provider]);
  value.connect.mockRejectedValueOnce(
    new ConnectError("response lost", Code.Unavailable),
  );
  render(
    value.view(<AccountConnection initial={account} active close={() => {}} />),
  );
  const key = screen.getByLabelText("API key");
  await waitFor(() => expect(key.matches(":disabled")).toBe(false));
  fireEvent.change(key, { target: { value: "fixture-only-secret" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect API key" }));
  const retry = await screen.findByRole("button", {
    name: "Retry the same connection",
  });
  expect((key as HTMLInputElement).value).toBe("");
  expect(
    JSON.stringify(
      value.client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    ),
  ).not.toContain("fixture-only-secret");
  fireEvent.click(retry);
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(2));
  expect(value.connect.mock.calls[0][0]).toEqual(
    value.connect.mock.calls[1][0],
  );
  expect(value.connect.mock.calls[0][0]).toMatchObject({
    mutation: { expectedRevision: 5n },
    keyless: false,
  });
  expect(
    new TextDecoder().decode(
      (value.connect.mock.calls[0][0] as { apiKey: Uint8Array }).apiKey,
    ),
  ).toBe("fixture-only-secret");
  await waitFor(() =>
    expect(value.client.getMutationCache().getAll()).toHaveLength(0),
  );
});

it("resumes pending credential cleanup using the original server-retained mutation", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "Provider",
    authentication: "bearer",
  });
  const requestId = newRequestId();
  const account = resource(
    EntityKind.ACCOUNT,
    {
      alias: "Cleanup",
      provider_id: provider.id,
      type: "api",
      health: "disconnected",
      removal: { request_id: requestId, expected_revision: 8 },
    },
    12n,
  );
  const value = fixture([account, provider]);
  render(
    value.view(<AccountConnection initial={account} active close={() => {}} />),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry original credential cleanup" }),
  );
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0]).toMatchObject({
    mutation: { id: account.id, requestId, expectedRevision: 8n },
  });
  expect(value.connect).not.toHaveBeenCalled();
});

it("preserves explicit empty restrictions and requires a primary repository after removal", async () => {
  const first = resource(EntityKind.REPOSITORY, { name: "First" }),
    second = resource(EntityKind.REPOSITORY, { name: "Second" });
  const project = resource(EntityKind.PROJECT, {
    name: "Project",
    repositories: [first.id, second.id],
    primary_repository: first.id,
    agents: { configured: false, ids: [] },
    accounts: { configured: false, ids: [] },
  });
  const value = fixture([project, first, second]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.PROJECT}
        initial={project}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.click(screen.getByRole("tab", { name: "Access" }));
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Restrict ai accounts" }),
  );
  fireEvent.click(screen.getByRole("tab", { name: "Repositories" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove entry 1" }));
  expect(
    (
      screen.getByRole("combobox", {
        name: "Primary repository",
      }) as HTMLSelectElement
    ).value,
  ).toBe("");
  fireEvent.change(
    screen.getByRole("combobox", { name: "Primary repository" }),
    { target: { value: second.id } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Project" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(
    JSON.parse(
      new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson),
    ),
  ).toMatchObject({
    repositories: [second.id],
    primary_repository: second.id,
    accounts: { configured: true, ids: [] },
    agents: { configured: false, ids: [] },
  });
});

it("completes a verified repository save once after queued and uncertain observations", async () => {
  const repository = resource(EntityKind.REPOSITORY, {
    name: "Repository",
    remote_url: "https://github.com/fixture/repo.git",
    checkouts: [{ machine_id: newRequestId(), path: "/owned/checkout" }],
    base: {},
    starting: {},
    auto_fetch: true,
  });
  const job = resource(EntityKind.JOB, {
    type: "save-repository",
    state: "queued",
  });
  const value = fixture([repository, job]),
    saved = vi.fn();
  value.save.mockImplementation(async (request) => ({
    job,
    requestId: input(request).mutation.requestId,
  }));
  render(
    <StrictMode>
      <NotificationProvider>
        {value.view(
          <SettingsLifetime>
            {() => (
              <ConfigurationEditor
                kind={EntityKind.REPOSITORY}
                initial={repository}
                active
                saved={saved}
                cancel={() => {}}
              />
            )}
          </SettingsLifetime>,
        )}
      </NotificationProvider>
    </StrictMode>,
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Save Repository",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await screen.findByText(
    "Accepted by the server. Waiting for the selected Worker to finish.",
  );
  expect(saved).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
  value.resources[1] = create(ResourceSchema, {
    ...job,
    revision: 2n,
    documentJson: encode({
      type: "save-repository",
      state: "uncertain",
      problem: { message: "Owned operation needs recovery" },
    }),
  });
  await act(async () => {
    await value.client.invalidateQueries();
  });
  await screen.findByText(
    "The Worker outcome is uncertain. Inspect the original operation before starting another.",
  );
  expect(value.save).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Return to retained draft" }),
  ).toBeNull();
  value.resources[0] = create(ResourceSchema, { ...repository, revision: 2n });
  value.resources[1] = create(ResourceSchema, {
    ...job,
    revision: 3n,
    documentJson: encode({
      type: "save-repository",
      state: "succeeded",
      output: { id: repository.id, revision: 2 },
    }),
  });
  await act(async () => {
    await value.client.invalidateQueries();
  });
  expect(await screen.findByText("Repository saved.")).toBeTruthy();
  await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
  expect(
    screen.queryByText("Configuration saved after Worker validation."),
  ).toBeNull();
  await act(async () => {
    await value.client.invalidateQueries();
  });
  expect(saved).toHaveBeenCalledTimes(1);
  expect(screen.getAllByText("Repository saved.")).toHaveLength(1);
  expect(value.save).toHaveBeenCalledTimes(1);
});

function repositorySaveFixture() {
  const repository = resource(EntityKind.REPOSITORY, {
    name: "Repository",
    remote_url: "https://github.com/fixture/repo.git",
  });
  const job = resource(EntityKind.JOB, {
    type: "save-repository",
    state: "queued",
  });
  const saved = vi.fn();
  const value = fixture([repository, job]);
  value.save.mockImplementation(async (request) => ({
    job,
    requestId: input(request).mutation.requestId,
  }));
  const completion = (output: Document, extra: Document = {}) =>
    create(ResourceSchema, {
      ...job,
      revision: 2n,
      documentJson: encode({
        type: "save-repository",
        state: "succeeded",
        output,
        ...extra,
      }),
    });
  return { repository, job, saved, value, completion };
}

async function submitRepositorySave() {
  const button = await screen.findByRole("button", { name: "Save Repository" });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(button);
  await screen.findByText(
    "Accepted by the server. Waiting for the selected Worker to finish.",
  );
}

it.each(["missing", "different"] as const)(
  "does not complete a repository save when the acknowledged job type is %s",
  async (typeMode) => {
    const { repository, job, saved, value } = repositorySaveFixture();
    const suppliedType =
      typeMode === "missing" ? undefined : "inspect-repository";
    const supplied = create(ResourceSchema, {
      ...job,
      documentJson: encode({
        ...(suppliedType ? { type: suppliedType } : {}),
        state: "queued",
      }),
    });
    value.resources[1] = supplied;
    value.save.mockImplementation(async (request) => ({
      job: supplied,
      requestId: input(request).mutation.requestId,
    }));
    render(
      <NotificationProvider>
        {value.view(
          <ConfigurationEditor
            kind={EntityKind.REPOSITORY}
            initial={repository}
            active
            saved={saved}
            cancel={() => {}}
          />,
        )}
      </NotificationProvider>,
    );
    await submitRepositorySave();
    value.resources[1] = create(ResourceSchema, {
      ...supplied,
      revision: 2n,
      documentJson: encode({
        type: suppliedType ?? "save-repository",
        state: "succeeded",
        output: { id: repository.id, revision: 2 },
      }),
    });
    await act(async () => {
      await value.client.invalidateQueries();
    });
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    expect(
      screen.getByText(
        "Accepted by the server. Waiting for the selected Worker to finish.",
      ),
    ).toBeTruthy();
    value.client.clear();
  },
);

it.each(["missing", "foreign", "equal", "older", "unsafe", "problem"])(
  "requires verified advanced original repository output after a %s completion",
  async (failure) => {
    const { repository, saved, value, completion } = repositorySaveFixture();
    render(
      <NotificationProvider>
        {value.view(
          <SettingsLifetime>
            {() => (
              <ConfigurationEditor
                kind={EntityKind.REPOSITORY}
                initial={repository}
                active
                saved={saved}
                cancel={() => {}}
              />
            )}
          </SettingsLifetime>,
        )}
      </NotificationProvider>,
    );
    await submitRepositorySave();
    const output =
      failure === "missing"
        ? {}
        : {
            id: failure === "foreign" ? newRequestId() : repository.id,
            revision:
              failure === "equal"
                ? 1
                : failure === "older"
                  ? 0
                  : failure === "unsafe"
                    ? "18446744073709551616"
                    : 2,
          };
    value.resources[1] = completion(
      output,
      failure === "problem" ? { problem: { message: "Unverified" } } : {},
    );
    await act(async () => {
      await value.client.invalidateQueries();
    });
    await screen.findByText(
      "The repository save could not be verified. Read the original status again before continuing.",
    );
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    const retry = screen.getByRole("button", {
      name: "Retry original status read",
    });
    value.resources[1] = create(ResourceSchema, {
      ...completion({ id: repository.id, revision: "2" }),
      revision: 3n,
    });
    value.resources[0] = create(ResourceSchema, {
      ...repository,
      revision: 2n,
    });
    fireEvent.click(retry);
    expect(await screen.findByText("Repository saved.")).toBeTruthy();
    expect(saved).toHaveBeenCalledTimes(1);
    expect(value.save).toHaveBeenCalledTimes(1);
  },
);

it.each(["failed", "foreign", "older", "malformed"])(
  "does not trust succeeded fallback during a %s original status read",
  async (failure) => {
    const { repository, saved, value, completion } = repositorySaveFixture();
    const accepted = completion({ id: repository.id, revision: 2 });
    let trustworthy = false;
    let submitted = false;
    let reads = 0;
    const transportValue = fixture([repository, accepted], {
      readResource: (id) => {
        if (id !== accepted.id) {
          if (!submitted) return { resource: repository };
          return {
            resource: create(ResourceSchema, { ...repository, revision: 2n }),
          };
        }
        reads += 1;
        if (trustworthy) return { resource: accepted };
        if (failure === "failed")
          throw new ConnectError("Status unavailable", Code.Unavailable);
        return {
          resource: create(ResourceSchema, {
            ...accepted,
            id: failure === "foreign" ? newRequestId() : accepted.id,
            revision: failure === "older" ? 1n : accepted.revision,
            documentJson:
              failure === "malformed"
                ? encode({ type: "save-repository", state: "future" })
                : accepted.documentJson,
          }),
        };
      },
    });
    transportValue.save.mockImplementation(async (request) => {
      submitted = true;
      return {
        job: accepted,
        requestId: input(request).mutation.requestId,
      };
    });
    render(
      <NotificationProvider>
        {transportValue.view(
          <SettingsLifetime>
            {() => (
              <ConfigurationEditor
                kind={EntityKind.REPOSITORY}
                initial={repository}
                active
                saved={saved}
                cancel={() => {}}
              />
            )}
          </SettingsLifetime>,
        )}
      </NotificationProvider>,
    );
    const button = await screen.findByRole("button", {
      name: "Save Repository",
    });
    await waitFor(() =>
      expect((button as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(button);
    await screen.findByText(
      "The repository save could not be verified. Read the original status again before continuing.",
    );
    await screen.findByRole("button", {
      name: "Retry original status read",
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Retry original status read",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    const retry = screen.getByRole("button", {
      name: "Retry original status read",
    });
    expect(reads).toBeGreaterThan(0);
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    trustworthy = true;
    fireEvent.click(retry);
    expect(await screen.findByText("Repository saved.")).toBeTruthy();
    expect(saved).toHaveBeenCalledTimes(1);
    expect(transportValue.save).toHaveBeenCalledTimes(1);
    expect(value.save).not.toHaveBeenCalled();
  },
);

it.each(["failed", "missing", "foreign", "kind", "schema", "zero", "older"])(
  "retains a successful repository save after a %s fresh read and retries only the read",
  async (failure) => {
    const { repository, job, saved, completion } = repositorySaveFixture();
    const fresh = create(ResourceSchema, { ...repository, revision: 3n });
    let trustworthy = false;
    let submitted = false;
    const completionRead = vi.fn();
    const read = vi.fn((id: string) => {
      if (id === job.id) return { resource: value.resources[1] };
      // The editor checks its submitted revision before completion starts.
      if (!submitted) return { resource: repository };
      completionRead();
      if (trustworthy) return { resource: fresh };
      if (failure === "failed")
        throw new ConnectError("Private read diagnostic", Code.Unavailable);
      if (failure === "missing") return {};
      return {
        resource: create(ResourceSchema, {
          ...fresh,
          id: failure === "foreign" ? newRequestId() : repository.id,
          kind: failure === "kind" ? EntityKind.PROJECT : EntityKind.REPOSITORY,
          schemaVersion: failure === "schema" ? 999 : fresh.schemaVersion,
          revision: failure === "zero" ? 0n : failure === "older" ? 2n : 3n,
        }),
      };
    });
    const value = fixture([repository, job], { readResource: read });
    value.save.mockImplementation(async (request) => {
      submitted = true;
      return { job, requestId: input(request).mutation.requestId };
    });
    render(
      <StrictMode>
        <NotificationProvider>
          {value.view(
            <SettingsLifetime>
              {() => (
                <ConfigurationEditor
                  kind={EntityKind.REPOSITORY}
                  initial={repository}
                  active
                  saved={saved}
                  cancel={() => {}}
                />
              )}
            </SettingsLifetime>,
          )}
        </NotificationProvider>
      </StrictMode>,
    );
    await submitRepositorySave();
    expect(completionRead).not.toHaveBeenCalled();
    value.resources[1] = completion({ id: repository.id, revision: "3" });
    await act(async () => {
      await value.client.invalidateQueries();
    });
    const retry = await screen.findByRole("button", {
      name: "Retry current read",
    });
    await waitFor(() =>
      expect((retry as HTMLButtonElement).disabled).toBe(false),
    );
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    if (failure !== "failed")
      expect(
        screen.getByText(
          "The saved repository identity or revision could not be verified. Inspect the original operation before continuing.",
        ),
      ).toBeTruthy();
    const cached = value.client
      .getQueryCache()
      .getAll()
      .find((query) => {
        const key = query.queryKey[1] as
          { input?: { id?: string } } | undefined;
        return key?.input?.id === repository.id;
      });
    expect(cached).toBeTruthy();
    await act(async () => {
      value.client.setQueryData(cached!.queryKey, { resource: fresh });
    });
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    const statusReads = read.mock.calls.filter(([id]) => id === job.id).length;
    trustworthy = true;
    fireEvent.click(retry);
    expect(await screen.findByText("Repository saved.")).toBeTruthy();
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(read.mock.calls.filter(([id]) => id === job.id)).toHaveLength(
      statusReads,
    );
    expect(value.save).toHaveBeenCalledTimes(1);
  },
);

it.each(["complete", "inactive", "disposed", "unmounted", "output-replaced"])(
  "fences a pending repository completion read when its owner is %s",
  async (transition) => {
    const { repository, job, saved, completion } = repositorySaveFixture();
    const fresh = create(ResourceSchema, { ...repository, revision: 2n });
    let resolve!: (result: { resource: Resource }) => void;
    const pending = new Promise<{ resource: Resource }>((done) => {
      resolve = done;
    });
    let resolveReplacement!: (result: { resource: Resource }) => void;
    const replacement = new Promise<{ resource: Resource }>((done) => {
      resolveReplacement = done;
    });
    let reads = 0;
    let submitted = false;
    const value = fixture([repository, job], {
      readResource: (id) => {
        if (id === job.id) return { resource: value.resources[1] };
        if (!submitted) return { resource: repository };
        reads += 1;
        return transition === "output-replaced" && reads > 1
          ? replacement
          : pending;
      },
    });
    value.save.mockImplementation(async (request) => {
      submitted = true;
      return { job, requestId: input(request).mutation.requestId };
    });
    let opening!: SettingsOpening;
    const view = (active: boolean) => (
      <NotificationProvider>
        {value.view(
          <SettingsLifetime>
            {(current) => {
              opening = current;
              return (
                <ConfigurationEditor
                  kind={EntityKind.REPOSITORY}
                  initial={repository}
                  active={active}
                  saved={saved}
                  cancel={() => {}}
                />
              );
            }}
          </SettingsLifetime>,
        )}
      </NotificationProvider>
    );
    const mounted = render(view(true));
    await submitRepositorySave();
    value.resources[1] = completion({ id: repository.id, revision: 2 });
    await act(async () => {
      // Refresh only the job; the baseline editor query has its own reader.
      await value.client.invalidateQueries({
        predicate: (query) => {
          const key = query.queryKey[1] as
            { input?: { id?: string } } | undefined;
          return key?.input?.id === job.id;
        },
      });
    });
    await waitFor(() => expect(reads).toBeGreaterThan(0));
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    if (transition === "inactive") mounted.rerender(view(false));
    else if (transition === "disposed") opening.dispose(value.client);
    else if (transition === "unmounted") mounted.unmount();
    else if (transition === "output-replaced") {
      value.resources[1] = create(ResourceSchema, {
        ...completion({ id: repository.id, revision: 3 }),
        revision: 3n,
      });
      await act(async () => {
        // Refresh only the job; the baseline editor query has its own reader.
        await value.client.invalidateQueries({
          predicate: (query) => {
            const key = query.queryKey[1] as
              { input?: { id?: string } } | undefined;
            return key?.input?.id === job.id;
          },
        });
      });
    }
    if (transition === "output-replaced")
      await waitFor(() => expect(reads).toBe(2));
    await act(async () => {
      resolve({
        resource:
          transition === "output-replaced"
            ? create(ResourceSchema, { ...fresh, revision: 3n })
            : fresh,
      });
    });
    if (transition === "complete") {
      await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
      expect(screen.getAllByText("Repository saved.")).toHaveLength(1);
    } else {
      expect(saved).not.toHaveBeenCalled();
      expect(screen.queryByText("Repository saved.")).toBeNull();
    }
    if (transition === "output-replaced") {
      await act(async () => {
        resolveReplacement({
          resource: create(ResourceSchema, { ...fresh, revision: 3n }),
        });
      });
      await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    }
    expect(value.save).toHaveBeenCalledTimes(1);
  },
);

it.each(["inactive", "disposed", "unmounted"])(
  "fences repository completion after its original opening is %s",
  async (departure) => {
    const { repository, saved, value, completion } = repositorySaveFixture();
    let opening!: SettingsOpening;
    const view = (active: boolean) => (
      <NotificationProvider>
        {value.view(
          <SettingsLifetime>
            {(current) => {
              opening = current;
              return (
                <ConfigurationEditor
                  kind={EntityKind.REPOSITORY}
                  initial={repository}
                  active={active}
                  saved={saved}
                  cancel={() => {}}
                />
              );
            }}
          </SettingsLifetime>,
        )}
      </NotificationProvider>
    );
    const mounted = render(view(true));
    await submitRepositorySave();
    if (departure === "inactive") mounted.rerender(view(false));
    else if (departure === "disposed") opening.dispose(value.client);
    else mounted.unmount();
    value.resources[1] = completion({ id: repository.id, revision: 2 });
    await act(async () => {
      await value.client.invalidateQueries();
    });
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  },
);

it("closes a verified repository edit, refreshes its inventory, and restores the opener", async () => {
  const { repository, value, completion } = repositorySaveFixture();
  render(
    <NotificationProvider>{value.view(<Settings />)}</NotificationProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  const opener = await screen.findByRole("button", {
    name: `Edit Repository`,
  });
  opener.focus();
  fireEvent.click(opener);
  await submitRepositorySave();
  const reads = value.list.mock.calls.length;
  value.resources[0] = create(ResourceSchema, { ...repository, revision: 2n });
  value.resources[1] = completion({ id: repository.id, revision: 2 });
  await act(async () => {
    await value.client.invalidateQueries();
  });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(await screen.findByText("Repository saved.")).toBeTruthy();
  expect(value.list.mock.calls.length).toBeGreaterThan(reads);
  await waitFor(() => expect(document.activeElement).toBe(opener));
  expect(value.save).toHaveBeenCalledTimes(1);
  fireEvent.click(opener);
  const save = await screen.findByRole("button", { name: "Save Repository" });
  await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(save);
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
});

it("keeps a successor repository edit open when a dismissed pending status read succeeds", async () => {
  const { repository, job, completion } = repositorySaveFixture();
  let pending = false;
  let resolve!: (result: { resource: Resource }) => void;
  const late = new Promise<{ resource: Resource }>((done) => {
    resolve = done;
  });
  const read = vi.fn((id: string) =>
    id === job.id && pending
      ? late
      : { resource: id === job.id ? job : repository },
  );
  const value = fixture([repository, job], { readResource: read });
  value.save.mockImplementation(async (request) => ({
    job,
    requestId: input(request).mutation.requestId,
  }));
  render(
    <NotificationProvider>{value.view(<Settings />)}</NotificationProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  const opener = await screen.findByRole("button", {
    name: `Edit Repository`,
  });
  fireEvent.click(opener);
  await submitRepositorySave();
  const reads = read.mock.calls.length;
  pending = true;
  await act(async () => {
    void value.client.invalidateQueries();
  });
  await waitFor(() => expect(read.mock.calls.length).toBeGreaterThan(reads));
  fireEvent.click(
    screen.getByRole("button", { name: "Close Edit Repository" }),
  );
  fireEvent.click(opener);
  const successor = screen.getByRole("dialog");
  await waitFor(() =>
    expect(successor.contains(document.activeElement)).toBe(true),
  );
  const focus = document.activeElement;
  await act(async () => {
    resolve({ resource: completion({ id: repository.id, revision: 2 }) });
  });
  expect(screen.getByRole("dialog")).toBe(successor);
  expect(screen.queryByText("Repository saved.")).toBeNull();
  expect(document.activeElement).toBe(focus);
  expect(value.save).toHaveBeenCalledTimes(1);
});

function repositoryNameConflict() {
  return new ConnectError("Collision", Code.Aborted, undefined, [
    {
      desc: ErrorDetailSchema,
      value: create(ErrorDetailSchema, {
        code: "conflict",
        cause: "configuration_name_conflict",
      }),
    },
  ]);
}
const repositoryNameMessage =
  "A repository with this name already exists. Choose another name.";
function ReopenableRepositoryEditor({
  initial,
  saved = () => {},
}: {
  initial: Resource;
  saved?: () => void;
}) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button onClick={() => setOpen((value) => !value)}>Toggle editor</button>
      {open ? (
        <ConfigurationEditor
          kind={EntityKind.REPOSITORY}
          initial={initial}
          active
          saved={saved}
          cancel={() => {}}
        />
      ) : null}
    </>
  );
}
function SwitchableRepositoryEditor({
  repositories,
}: {
  repositories: Resource[];
}) {
  const [index, setIndex] = useState(0);
  return (
    <>
      {repositories.map((_, position) => (
        <button key={position} onClick={() => setIndex(position)}>
          {`Show repository ${position}`}
        </button>
      ))}
      <ConfigurationEditor
        key={repositories[index].id}
        kind={EntityKind.REPOSITORY}
        initial={repositories[index]}
        active
        saved={() => {}}
        cancel={() => {}}
      />
    </>
  );
}
async function renameRepository(name: string) {
  const field = (await screen.findByRole("textbox", {
    name: "Name",
  })) as HTMLInputElement;
  fireEvent.change(field, { target: { value: name } });
  const save = screen.getByRole("button", { name: "Save Repository" });
  await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(save);
  return field;
}
function reopen() {
  fireEvent.click(screen.getByRole("button", { name: "Toggle editor" }));
  fireEvent.click(screen.getByRole("button", { name: "Toggle editor" }));
}

describe("reopened repository editors", () => {
  const saved = () =>
    resource(EntityKind.REPOSITORY, {
      name: "Alpha",
      remote_url: "https://github.com/fixture/repo.git",
    });
  it("keeps a rejected name conflict while mounted and clears it for a reopened editor", async () => {
    const repository = saved();
    const value = fixture([repository]);
    value.save.mockRejectedValueOnce(repositoryNameConflict());
    render(
      <NotificationProvider>
        {value.view(<ReopenableRepositoryEditor initial={repository} />)}
      </NotificationProvider>,
    );
    const field = await renameRepository(" Beta ");
    await screen.findByText(repositoryNameMessage);
    expect(field.value).toBe(" Beta ");
    reopen();
    const restored = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(restored.value).toBe("Alpha");
    expect(screen.queryByText(repositoryNameMessage)).toBeNull();
    expect(restored.getAttribute("aria-invalid")).not.toBe("true");
    // A rejection observed after the reopen is still presented.
    value.save.mockRejectedValueOnce(repositoryNameConflict());
    await renameRepository("Beta");
    await screen.findByText(repositoryNameMessage);
    expect(value.save).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["denied", () => new ConnectError("Denied fixture", Code.PermissionDenied)],
    [
      "untyped aborted",
      () => new ConnectError("Aborted fixture", Code.Aborted),
    ],
  ])(
    "keeps a definitive %s rejection in the generic presentation when an editor reopens",
    async (_name, failure) => {
      const repository = saved();
      const value = fixture([repository]);
      value.save.mockRejectedValueOnce(failure());
      render(
        <NotificationProvider>
          {value.view(<ReopenableRepositoryEditor initial={repository} />)}
        </NotificationProvider>,
      );
      await renameRepository("Beta");
      await waitFor(() =>
        expect(document.querySelector(".problem")).not.toBeNull(),
      );
      const before = document.querySelector(".problem")?.textContent;
      reopen();
      const restored = (await screen.findByRole("textbox", {
        name: "Name",
      })) as HTMLInputElement;
      // The saved document returns, while the generic error stays visible and
      // the registry entry is not cleared by the reopen.
      expect(restored.value).toBe("Alpha");
      expect(document.querySelector(".problem")?.textContent).toBe(before);
      expect(screen.queryByText(repositoryNameMessage)).toBeNull();
      expect(restored.getAttribute("aria-invalid")).not.toBe("true");
      expect(value.save).toHaveBeenCalledTimes(1);
    },
  );

  it("keeps a late rejection that arrives after the editor reopened while busy", async () => {
    const repository = saved();
    const value = fixture([repository]);
    let reject!: (error: unknown) => void;
    value.save.mockImplementationOnce(
      () =>
        new Promise((_, no) => {
          reject = no;
        }),
    );
    render(
      <NotificationProvider>
        {value.view(<ReopenableRepositoryEditor initial={repository} />)}
      </NotificationProvider>,
    );
    await renameRepository("Beta");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    reopen();
    expect(
      (
        (await screen.findByRole("button", {
          name: /Save Repository/,
        })) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await act(async () => {
      reject(repositoryNameConflict());
    });
    await screen.findByText(repositoryNameMessage);
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("keeps the original uncertain request when an editor reopens", async () => {
    const repository = saved();
    const value = fixture([repository]);
    value.save.mockRejectedValueOnce(
      new ConnectError("Original unavailable save", Code.Unavailable),
    );
    render(
      <NotificationProvider>
        {value.view(<ReopenableRepositoryEditor initial={repository} />)}
      </NotificationProvider>,
    );
    await renameRepository("Beta");
    await screen.findByRole("button", {
      name: "Retry the same configuration",
    });
    const original = input(value.save.mock.calls[0][0]);
    reopen();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Retry the same configuration",
      }),
    );
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
    const retried = input(value.save.mock.calls[1][0]);
    expect(retried.mutation.requestId).toBe(original.mutation.requestId);
    expect(retried.documentJson).toEqual(original.documentJson);
  });

  const acceptedHint =
    "Accepted by the server. Waiting for the selected Worker to finish.";
  function acceptedJobFixture() {
    const repository = saved();
    const job = resource(EntityKind.JOB, {
      type: "save-repository",
      state: "queued",
    });
    const value = fixture([repository, job]);
    value.save.mockImplementation(async (request) => ({
      job,
      requestId: input(request).mutation.requestId,
    }));
    const settle = async (document: Document) => {
      value.resources[1] = create(ResourceSchema, {
        ...job,
        revision: 2n,
        documentJson: encode({ type: "save-repository", ...document }),
      });
      await act(async () => {
        await value.client.invalidateQueries();
      });
    };
    const mount = (onSaved?: () => void) =>
      render(
        <NotificationProvider>
          {value.view(
            <SettingsLifetime>
              {() => (
                <ReopenableRepositoryEditor
                  initial={repository}
                  saved={onSaved}
                />
              )}
            </SettingsLifetime>,
          )}
        </NotificationProvider>,
      );
    return { repository, job, value, settle, mount };
  }
  const collision = {
    state: "failed",
    problem: {
      code: "conflict",
      cause: "configuration_name_conflict",
      message: "Collision",
    },
  };

  it("keeps an accepted job owned by the connection when an editor reopens", async () => {
    const { value, mount } = acceptedJobFixture();
    mount();
    await renameRepository("Beta");
    await screen.findByText(acceptedHint);
    reopen();
    // The original job stays authoritative: no draft form and no new write.
    await screen.findByText(acceptedHint);
    expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
    expect(screen.queryByText(repositoryNameMessage)).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("returns a deferred collision to the restored draft after the editor reopened while pending", async () => {
    const { value, settle, mount } = acceptedJobFixture();
    mount();
    await renameRepository(" Beta ");
    await screen.findByText(acceptedHint);
    reopen();
    await screen.findByText(acceptedHint);
    await settle(collision);
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe(" Beta ");
    await screen.findByText(repositoryNameMessage);
    expect(value.save).toHaveBeenCalledTimes(1);
    // The released draft is editable and the correction is a new save.
    fireEvent.change(field, { target: { value: "Gamma" } });
    expect(screen.queryByText(repositoryNameMessage)).toBeNull();
    const save = screen.getByRole("button", {
      name: "Save Repository",
    }) as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));
    fireEvent.click(save);
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
    const corrected = input(value.save.mock.calls[1][0]);
    expect(
      JSON.parse(new TextDecoder().decode(corrected.documentJson)).name,
    ).toBe("Gamma");
  });

  it("restores the draft when the collision settled while no editor was mounted", async () => {
    const { value, settle, mount } = acceptedJobFixture();
    mount();
    await renameRepository(" Beta ");
    await screen.findByText(acceptedHint);
    fireEvent.click(screen.getByRole("button", { name: "Toggle editor" }));
    expect(screen.queryByText(acceptedHint)).toBeNull();
    await settle(collision);
    fireEvent.click(screen.getByRole("button", { name: "Toggle editor" }));
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    await screen.findByText(repositoryNameMessage);
    expect(field.value).toBe(" Beta ");
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("requires a fresh read before a restored successful job completes", async () => {
    const { repository, job, value, settle, mount } = acceptedJobFixture();
    const onSaved = vi.fn();
    mount(onSaved);
    await renameRepository("Beta");
    await screen.findByText(acceptedHint);
    reopen();
    await screen.findByText(acceptedHint);
    value.resources[0] = create(ResourceSchema, {
      ...repository,
      revision: 2n,
    });
    await settle({
      state: "succeeded",
      output: { id: repository.id, revision: 2 },
    });
    expect(await screen.findByText("Repository saved.")).toBeTruthy();
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(value.save).toHaveBeenCalledTimes(1);
    expect(job.id).toBeTruthy();
  });

  it("keeps the review path for a generic failure of a restored job", async () => {
    const { value, settle, mount } = acceptedJobFixture();
    mount();
    await renameRepository(" Beta ");
    await screen.findByText(acceptedHint);
    reopen();
    await screen.findByText(acceptedHint);
    await settle({ state: "failed" });
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Read and review repository settings",
      }),
    );
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe(" Beta ");
    expect(screen.queryByText(repositoryNameMessage)).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  // The original SaveConfiguration response is held until `accept`, so the
  // editor can be reconstructed while the request is still pending.
  function heldJobFixture() {
    const fixed = acceptedJobFixture();
    let open!: () => void;
    const gate = new Promise<void>((resolve) => {
      open = resolve;
    });
    fixed.value.save.mockImplementationOnce(async (request) => {
      await gate;
      return {
        job: fixed.job,
        requestId: input(request).mutation.requestId,
      };
    });
    const accept = async () => {
      await act(async () => {
        open();
        await gate;
      });
    };
    return { ...fixed, accept };
  }

  it("observes a job accepted after the editor reopened while pending", async () => {
    const { value, accept, mount } = heldJobFixture();
    mount();
    await renameRepository("Beta");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    reopen();
    expect(screen.queryByText(acceptedHint)).toBeNull();
    await accept();
    // The reopened editor mounts the job observer without another remount.
    await screen.findByText(acceptedHint);
    expect(screen.queryByRole("textbox", { name: "Name" })).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("returns a collision to the draft for a job accepted after the editor reopened while pending", async () => {
    const { value, settle, accept, mount } = heldJobFixture();
    mount();
    await renameRepository(" Beta ");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    reopen();
    await accept();
    await screen.findByText(acceptedHint);
    await settle(collision);
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe(" Beta ");
    await screen.findByText(repositoryNameMessage);
    expect(value.save).toHaveBeenCalledTimes(1);
    // The released job is not adopted again and the correction is a new save.
    expect(screen.queryByText(acceptedHint)).toBeNull();
    fireEvent.change(field, { target: { value: "Gamma" } });
    const save = screen.getByRole("button", {
      name: "Save Repository",
    }) as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));
    fireEvent.click(save);
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  });

  it("completes a job accepted after the editor reopened only after the fresh read, once", async () => {
    const { repository, value, settle, accept, mount } = heldJobFixture();
    const onSaved = vi.fn();
    mount(onSaved);
    await renameRepository("Beta");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    reopen();
    await accept();
    await screen.findByText(acceptedHint);
    value.resources[0] = create(ResourceSchema, {
      ...repository,
      revision: 2n,
    });
    await settle({
      state: "succeeded",
      output: { id: repository.id, revision: 2 },
    });
    expect(await screen.findByText("Repository saved.")).toBeTruthy();
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("does not complete a job accepted after the editor reopened without the expected Repository revision", async () => {
    const { value, settle, accept, mount } = heldJobFixture();
    const onSaved = vi.fn();
    mount(onSaved);
    await renameRepository("Beta");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    reopen();
    await accept();
    await screen.findByText(acceptedHint);
    // The job succeeded, but the fresh Repository read still has the old
    // revision, so the save must not report completion.
    await settle({
      state: "succeeded",
      output: { id: value.resources[0].id, revision: 2 },
    });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.queryByText("Repository saved.")).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("keeps the review path for a generic failure of a job accepted after the editor reopened", async () => {
    const { value, settle, accept, mount } = heldJobFixture();
    mount();
    await renameRepository(" Beta ");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    reopen();
    await accept();
    await screen.findByText(acceptedHint);
    await settle({ state: "failed" });
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Read and review repository settings",
      }),
    );
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe(" Beta ");
    expect(screen.queryByText(repositoryNameMessage)).toBeNull();
    expect(screen.queryByText(acceptedHint)).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("does not adopt a late job in another repository's editor", async () => {
    const first = saved();
    const second = resource(EntityKind.REPOSITORY, {
      name: "Second",
      remote_url: "https://github.com/fixture/second.git",
    });
    const queued = resource(EntityKind.JOB, {
      type: "save-repository",
      state: "queued",
    });
    const value = fixture([first, second, queued]);
    let open!: () => void;
    const gate = new Promise<void>((resolve) => {
      open = resolve;
    });
    value.save.mockImplementationOnce(async (request) => {
      await gate;
      return { job: queued, requestId: input(request).mutation.requestId };
    });
    render(
      <NotificationProvider>
        {value.view(
          <SwitchableRepositoryEditor repositories={[first, second]} />,
        )}
      </NotificationProvider>,
    );
    await renameRepository("Beta");
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: "Show repository 1" }));
    await act(async () => {
      open();
      await gate;
    });
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe("Second");
    expect(screen.queryByText(acceptedHint)).toBeNull();
    expect(value.save).toHaveBeenCalledTimes(1);
  });

  it("restores the submitted draft for an uncertain save and retries the original request", async () => {
    const repository = saved();
    const value = fixture([repository]);
    value.save.mockRejectedValueOnce(
      new ConnectError("Original unavailable save", Code.Unavailable),
    );
    render(
      <NotificationProvider>
        {value.view(<ReopenableRepositoryEditor initial={repository} />)}
      </NotificationProvider>,
    );
    await renameRepository(" Beta ");
    await screen.findByRole("button", {
      name: "Retry the same configuration",
    });
    reopen();
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe(" Beta ");
    fireEvent.click(
      screen.getByRole("button", { name: "Retry the same configuration" }),
    );
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  });

  it("does not hand a retained save to another repository", async () => {
    const first = saved();
    const second = resource(EntityKind.REPOSITORY, {
      name: "Second",
      remote_url: "https://github.com/fixture/second.git",
    });
    const { job, value } = (() => {
      const queued = resource(EntityKind.JOB, {
        type: "save-repository",
        state: "queued",
      });
      const fixed = fixture([first, second, queued]);
      fixed.save.mockImplementation(async (request) => ({
        job: queued,
        requestId: input(request).mutation.requestId,
      }));
      return { job: queued, value: fixed };
    })();
    render(
      <NotificationProvider>
        {value.view(
          <SwitchableRepositoryEditor repositories={[first, second]} />,
        )}
      </NotificationProvider>,
    );
    await renameRepository("Beta");
    await screen.findByText(acceptedHint);
    fireEvent.click(screen.getByRole("button", { name: "Show repository 1" }));
    const field = (await screen.findByRole("textbox", {
      name: "Name",
    })) as HTMLInputElement;
    expect(field.value).toBe("Second");
    expect(screen.queryByText(acceptedHint)).toBeNull();
    expect(job.id).toBeTruthy();
    expect(value.save).toHaveBeenCalledTimes(1);
  });
});

it("preserves the generic configuration job Done step", async () => {
  const template = resource(EntityKind.TEMPLATE, {
    name: "Instructions",
    content: "Retained",
  });
  const job = resource(EntityKind.JOB, {
    type: "save-template",
    state: "succeeded",
  });
  const value = fixture([template, job]),
    saved = vi.fn();
  value.save.mockResolvedValueOnce({ job });
  render(
    <NotificationProvider>
      {value.view(
        <ConfigurationEditor
          kind={EntityKind.TEMPLATE}
          initial={template}
          active
          saved={saved}
          cancel={() => {}}
        />,
      )}
    </NotificationProvider>,
  );
  fireEvent.change(screen.getByLabelText("Instructions"), {
    target: { value: "Updated" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save Instructions" }));
  const done = await screen.findByRole("button", { name: "Done" });
  expect(saved).not.toHaveBeenCalled();
  expect(screen.queryByText("Repository saved.")).toBeNull();
  fireEvent.click(done);
  expect(saved).toHaveBeenCalledTimes(1);
});

it.each(["failed", "canceled"] as const)(
  "freshly reviews a %s repository save before returning to its retained draft",
  async (state) => {
    const repository = resource(EntityKind.REPOSITORY, {
      name: "Repository",
      remote_url: "https://github.com/fixture/repo.git",
    });
    const job = resource(EntityKind.JOB, {
      type: "save-repository",
      state: "queued",
    });
    const resources = [repository, job];
    let repositoryReads = 0;
    const value = fixture(resources, {
      readResource: (id) => {
        if (id === repository.id) repositoryReads += 1;
        return { resource: resources.find((row) => row.id === id) };
      },
    });
    const saved = vi.fn();
    value.save.mockImplementation(async (request) => {
      const requestId = input(request).mutation.requestId;
      return { job, requestId };
    });
    render(
      value.view(
        <ConfigurationEditor
          kind={EntityKind.REPOSITORY}
          initial={repository}
          active
          saved={saved}
          cancel={() => {}}
        />,
      ),
    );
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Retained draft" },
    });
    const initialSave = (await screen.findByRole("button", {
      name: "Save Repository",
    })) as HTMLButtonElement;
    await waitFor(() => expect(initialSave.disabled).toBe(false));
    fireEvent.click(initialSave);
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    await screen.findByText(
      "Accepted by the server. Waiting for the selected Worker to finish.",
    );
    const previousRepositoryReads = repositoryReads;

    resources[1] = create(ResourceSchema, {
      ...job,
      revision: 2n,
      documentJson: encode({ type: "save-repository", state }),
    });
    await act(async () => {
      await value.client.invalidateQueries();
    });
    const review = await screen.findByRole("button", {
      name: "Read and review repository settings",
    });
    await waitFor(() =>
      expect(repositoryReads).toBeGreaterThan(previousRepositoryReads),
    );
    expect(screen.getByText("Repository · revision 1")).toBeTruthy();
    expect(saved).not.toHaveBeenCalled();

    fireEvent.click(review);
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe(
      "Retained draft",
    );
    const save = screen.getByRole("button", {
      name: "Save Repository",
    }) as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));
    fireEvent.click(save);
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
    expect(saved).not.toHaveBeenCalled();
  },
);

it("keeps repository saving blocked and offers a retry when the capability check fails", async () => {
  const repository = resource(EntityKind.REPOSITORY, {
    name: "Repository",
    remote_url: "https://github.com/fixture/repo.git",
    checkouts: [{ machine_id: newRequestId(), path: "/owned/checkout" }],
    base: {},
    starting: {},
    auto_fetch: true,
  });
  const value = fixture([repository], {
    systemStatusError: new ConnectError("status unavailable", Code.Unavailable),
  });
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.REPOSITORY}
        initial={repository}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  const save = await screen.findByRole("button", { name: "Save Repository" });
  await waitFor(() => expect((save as HTMLButtonElement).disabled).toBe(true));
  expect(
    screen.queryByText(
      "Update the selected server before saving a repository.",
    ),
  ).toBeNull();
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "Retry repository capability check" }),
  ).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("keeps legacy checkout repository editing available without remote capability", async () => {
  const repository = resource(EntityKind.REPOSITORY, {
    name: "Legacy repository",
    checkouts: [{ machine_id: newRequestId(), path: "/owned/checkout" }],
    base: {},
    starting: {},
    auto_fetch: true,
  });
  const value = fixture([repository], { systemCapabilities: [] });
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.REPOSITORY}
        initial={repository}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Renamed legacy repository" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(
    new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson),
  );
  expect(saved).toEqual({
    name: "Renamed legacy repository",
    checkouts: [{ machine_id: expect.any(String), path: "/owned/checkout" }],
    base: {},
    starting: {},
    auto_fetch: true,
  });
  expect(saved).not.toHaveProperty("remote_url");
});

it("retries an original checkout inspection and uses only its owning Worker's canonical root", async () => {
  const machine = resource(EntityKind.MACHINE, { name: "Owned Worker" });
  const job = resource(EntityKind.JOB, {
    type: "inspect-repository",
    state: "succeeded",
    machine_id: machine.id,
    output: {
      root: "/canonical/checkout",
      remotes: ["origin"],
      default_refs: { origin: "main" },
    },
  });
  const value = fixture([machine, job]);
  value.inspect.mockRejectedValueOnce(
    new ConnectError("ack lost", Code.Unavailable),
  );
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.REPOSITORY}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Repository" },
  });
  fireEvent.change(screen.getByLabelText("Remote Git URL"), {
    target: { value: "https://github.com/fixture/repo.git" },
  });
  fireEvent.click(screen.getByRole("combobox", { name: "Runner Device" }));
  fireEvent.click(await screen.findByRole("option", { name: "Owned Worker" }));
  await waitFor(() =>
    expect(
      screen.getByRole("combobox", { name: "Runner Device" }).dataset.value,
    ).toBe(machine.id),
  );
  fireEvent.change(
    screen.getByLabelText("Absolute checkout path on this Worker"),
    { target: { value: "/alias/checkout" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Inspect checkout" }));
  const retry = await screen.findByRole("button", {
    name: "Retry the same inspection",
  });
  expect(
    (screen.getByRole("button", { name: "Cancel edit" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.click(retry);
  fireEvent.click(
    await screen.findByRole("button", { name: "Add inspected checkout" }),
  );
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Save Repository",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  expect(value.inspect.mock.calls[0][0]).toEqual(
    value.inspect.mock.calls[1][0],
  );
  expect(
    JSON.parse(
      new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson),
    ),
  ).toMatchObject({
    checkouts: [{ machine_id: machine.id, path: "/canonical/checkout" }],
    auto_fetch: true,
  });
});

it("retains the original revision and identity when a configuration deletion acknowledgment is lost", async () => {
  const project = resource(
      EntityKind.PROJECT,
      { name: "Retained history" },
      7n,
    ),
    value = fixture([project]);
  value.remove.mockRejectedValueOnce(
    new ConnectError("ack lost", Code.Unavailable),
  );
  render(
    value.view(
      <ConfigurationDeletion
        initial={project}
        deleted={() => {}}
        close={() => {}}
      />,
    ),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm configuration deletion" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same deletion" }),
  );
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[0][0]).toEqual(value.remove.mock.calls[1][0]);
  expect(value.remove.mock.calls[0][0]).toMatchObject({
    mutation: { id: project.id, expectedRevision: 7n },
  });
});

it("renders unknown quota and server candidate reasons without performing selection mutations", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Agent" }),
    project = resource(EntityKind.PROJECT, { name: "Restricted project" }),
    value = fixture([agent, project]);
  value.preview.mockResolvedValue({
    routeJson: encode({
      policy: "remaining-quota",
      fallback: true,
      candidates: [
        {
          id: newRequestId(),
          weight: 1,
          eligibility: "project-restricted",
          quota_state: "unknown",
        },
      ],
    }),
  });
  render(value.view(<RoutingPreview agent={agent} active close={() => {}} />));
  await screen.findByText("unknown", { exact: true });
  fireEvent.click(screen.getByRole("combobox", { name: "Project" }));
  fireEvent.click(
    await screen.findByRole("option", { name: "Restricted project" }),
  );
  await waitFor(() =>
    expect(value.preview).toHaveBeenLastCalledWith(
      expect.objectContaining({ agentId: agent.id, projectId: project.id }),
      expect.anything(),
    ),
  );
  expect(screen.getByText("No eligible account")).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("edits global routing preferences without rewriting unrelated policy or creating another singleton", async () => {
  const original = {
    default_routing: "sequential-exhaustion",
    automatic_fetch: true,
    notifications: false,
    remediation: {
      ci_failure: true,
      review_feedback: false,
      merge_conflict: true,
      conflict_strategy: "rebase",
      session_strategy: "dedicated",
      attempt_limit: 9,
      agent_id: newRequestId(),
      machine_id: newRequestId(),
    },
  };
  const preferences = resource(EntityKind.SETTINGS, original, 8n);
  const value = fixture([preferences]);
  value.save.mockRejectedValueOnce(
    new ConnectError("lost response", Code.Unavailable),
  );
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Project defaults" }));
  await screen.findByRole("form", { name: "Project defaults form" });
  expect(
    screen.queryByRole("button", { name: "New Server preferences" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: /Delete Server preferences/ }),
  ).toBeNull();
  fireEvent.change(screen.getByLabelText("Default account routing"), {
    target: { value: "priority" },
  });
  expect(
    screen.getByRole("checkbox", {
      name: "Allow automatic fetch before Worktree preparation",
    }),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry the same configuration" }),
  );
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  expect(value.save.mock.calls[0][0]).toEqual(value.save.mock.calls[1][0]);
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(8n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({
    ...original,
    default_routing: "priority",
  });
});

it("retains incompatible permission selections across harness changes until explicit clearing", async () => {
  const model = resource(EntityKind.MODEL, { name: "Fixture model" });
  const original = {
    name: "Original agent",
    harness: "codex",
    routes: [
      {
        model: { subscription_service: "chatgpt", native_id: "fixture-native" },
        accounts: [{ id: newRequestId(), weight: 1 }],
      },
    ],
    templates: [],
    options: {
      permission: "workspace-write",
      approval_policy: "on-request",
      future_option: "retained",
    },
  };
  const agent = resource(EntityKind.AGENT, original, 3n);
  const value = fixture([agent, model]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.AGENT}
        initial={agent}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Harness" }), {
    target: { value: "claude-code" },
  });
  expect(screen.getByRole("alert").textContent).toContain(
    "workspace-write · on-request",
  );
  expect(
    screen.queryByRole("combobox", { name: "Permission mode" }),
  ).toBeNull();
  expect(
    (
      screen.getByRole("textbox", {
        name: "Approval policy",
      }) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  await chooseScrollOption(
    screen.getByRole("combobox", { name: "Claude permission mode" }),
    "acceptEdits",
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Harness" }), {
    target: { value: "codex" },
  });
  expect(
    scrollChoiceValue(
      screen.getByRole("combobox", { name: "Permission mode" }),
    ),
  ).toBe("workspace-write");
  expect(
    (
      screen.getByRole("textbox", {
        name: "Approval policy",
      }) as HTMLInputElement
    ).value,
  ).toBe("on-request");
  expect(screen.getByRole("alert").textContent).toContain("acceptEdits");
  fireEvent.change(screen.getByRole("combobox", { name: "Harness" }), {
    target: { value: "claude-code" },
  });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Clear incompatible permission settings",
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Agent Worker" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(3n);
  expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({
    ...original,
    harness: "claude-code",
    options: {
      permission: "default",
      claude_permission: "acceptEdits",
      future_option: "retained",
    },
  });
});

it("shows unsupported stored Claude modes without replacing the retained draft", async () => {
  const model = resource(EntityKind.MODEL, { name: "Fixture model" });
  const agent = resource(EntityKind.AGENT, {
    name: "Future agent",
    harness: "claude-code",
    routes: [
      {
        model: { subscription_service: "claude", native_id: "fixture-native" },
        accounts: [{ id: newRequestId(), weight: 1 }],
      },
    ],
    options: { permission: "default", claude_permission: "future-mode" },
  });
  const value = fixture([agent, model]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.AGENT}
        initial={agent}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  const selector = screen.getByRole("combobox", {
    name: "Claude permission mode",
  });
  expect(scrollChoiceValue(selector)).toBe("future-mode");
  expect(selector.textContent).toBe("Unsupported selection · future-mode");
  await chooseScrollOption(selector, "bypassPermissions");
  expect(
    screen.getByText(/Bypass skips native permission prompts/),
  ).toBeTruthy();
  expect(value.save).not.toHaveBeenCalled();
});

it("saves remediation switches, exact reviewer IDs and explicit execution choices through configuration", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Fix agent" });
  const machine = resource(EntityKind.MACHINE, { name: "Fix machine" });
  const value = fixture([agent, machine]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.SETTINGS}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  expect(screen.getByText(/Enabled policies run bounded fixes/)).toBeTruthy();
  for (const name of [
    "Automatically fix required CI failures",
    "Automatically handle matching published feedback",
    "Automatically resolve verified merge conflicts",
  ]) {
    expect(
      (screen.getByRole("checkbox", { name }) as HTMLInputElement).checked,
    ).toBe(false);
    fireEvent.click(screen.getByRole("checkbox", { name }));
  }
  screen.getByText("Remediation details").closest("details")!.open = true;
  fireEvent.click(
    screen.getByRole("combobox", { name: "Remediation Agent Worker" }),
  );
  fireEvent.click(await screen.findByRole("option", { name: "Fix agent" }));
  await waitFor(() =>
    expect(
      screen.getByRole("combobox", { name: "Remediation Agent Worker" }).dataset
        .value,
    ).toBe(agent.id),
  );
  fireEvent.click(
    screen.getByRole("combobox", { name: "Remediation Runner Device" }),
  );
  fireEvent.click(await screen.findByRole("option", { name: "Fix machine" }));
  await waitFor(() =>
    expect(
      screen.getByRole("combobox", { name: "Remediation Runner Device" })
        .dataset.value,
    ).toBe(machine.id),
  );
  fireEvent.change(screen.getByLabelText("Remediation session strategy"), {
    target: { value: "dedicated" },
  });
  fireEvent.change(screen.getByLabelText("Conflict resolution strategy"), {
    target: { value: "rebase" },
  });
  fireEvent.change(
    screen.getByLabelText("Consecutive automatic attempt limit"),
    { target: { value: "7" } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Add reviewer selector" }),
  );
  fireEvent.change(screen.getByLabelText("Selector 1 type"), {
    target: { value: "bot" },
  });
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub numeric ID"), {
    target: { value: "9007199254740993" },
  });
  fireEvent.change(screen.getByLabelText("Selector 1 GitHub node ID"), {
    target: { value: "BOT_exact" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Add reviewer selector" }),
  );
  fireEvent.change(screen.getByLabelText("Selector 2 type"), {
    target: { value: "minimum-permission" },
  });
  fireEvent.change(screen.getByLabelText("Selector 2 minimum permission"), {
    target: { value: "MAINTAIN" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Save Project defaults" }),
  );
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(
    new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson),
  );
  expect(saved.remediation).toEqual({
    ci_failure: true,
    review_feedback: true,
    merge_conflict: true,
    conflict_strategy: "rebase",
    session_strategy: "dedicated",
    attempt_limit: 7,
    agent_id: agent.id,
    machine_id: machine.id,
    reviewer_selectors: [
      { kind: "bot", id: "9007199254740993", node_id: "BOT_exact" },
      { kind: "minimum-permission", permission: "MAINTAIN" },
    ],
  });
});

it("starts a repository override with automation off and removes it only through explicit inheritance", async () => {
  const repository = resource(EntityKind.REPOSITORY, {
    name: "Repository",
    remote_url: "https://github.com/fixture/repo.git",
    checkouts: [],
    base: {},
    starting: {},
    auto_fetch: true,
  });
  const value = fixture([repository]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.REPOSITORY}
        initial={repository}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  expect(
    screen.getByText(/inherits the complete server remediation policy/),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Set repository policy with automation off",
    }),
  );
  expect(
    (
      screen.getByLabelText(
        "Automatically fix required CI failures",
      ) as HTMLInputElement
    ).checked,
  ).toBe(false);
  expect(
    (
      screen.getByLabelText(
        "Consecutive automatic attempt limit",
      ) as HTMLInputElement
    ).value,
  ).toBe("3");
  expect(screen.queryByText("Remediation details")).toBeNull();
  fireEvent.click(
    screen.getByLabelText("Automatically fix required CI failures"),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Use server remediation policy" }),
  );
  expect(
    screen.queryByLabelText("Automatically fix required CI failures"),
  ).toBeNull();
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Save Repository",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const saved = JSON.parse(
    new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson),
  );
  expect(saved).toEqual({
    name: "Repository",
    remote_url: "https://github.com/fixture/repo.git",
    checkouts: [],
    base: {},
    starting: {},
    auto_fetch: true,
  });
});

it("retains empty repository selectors and clears incompatible identity fields when changing selector type", async () => {
  const policy = {
    ci_failure: false,
    review_feedback: true,
    merge_conflict: false,
    conflict_strategy: "merge",
    session_strategy: "reuse",
    attempt_limit: 3,
    reviewer_selectors: [{ kind: "app", id: "42", node_id: "A_exact" }],
  };
  const repository = resource(
    EntityKind.REPOSITORY,
    {
      name: "Repository",
      remote_url: "https://github.com/fixture/repo.git",
      checkouts: [],
      base: {},
      starting: {},
      auto_fetch: true,
      remediation: policy,
    },
    4n,
  );
  const value = fixture([repository]);
  render(
    value.view(
      <ConfigurationEditor
        kind={EntityKind.REPOSITORY}
        initial={repository}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.change(screen.getByLabelText("Selector 1 type"), {
    target: { value: "minimum-permission" },
  });
  expect(screen.queryByLabelText("Selector 1 GitHub numeric ID")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Remove reviewer selector 1" }),
  );
  expect(screen.getByText(/No reviewer selectors/)).toBeTruthy();
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Save Repository",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save Repository" }));
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
  const request = input(value.save.mock.calls[0][0]);
  expect(request.mutation.expectedRevision).toBe(4n);
  expect(
    JSON.parse(new TextDecoder().decode(request.documentJson)).remediation,
  ).toEqual({ ...policy, reviewer_selectors: [] });
});

it("keeps the unfiltered picker cursor independent and retains an exact provider absent from account filters", async () => {
  const openai = resource(EntityKind.PROVIDER, {
    name: "OpenAI",
    authentication: "bearer",
    protocol: "openai-responses",
    endpoint: "https://api.example.test/v1",
    enabled: true,
  });
  const anthropic = resource(EntityKind.PROVIDER, {
    name: "Anthropic",
    authentication: "bearer",
    protocol: "anthropic-messages",
    endpoint: "https://api.example.test/v1",
    enabled: true,
  });
  const custom = resource(EntityKind.PROVIDER, {
    name: "Custom API",
    authentication: "bearer",
    protocol: "openai-chat",
    endpoint: "https://custom.example.test/v1",
    enabled: true,
  });
  const entry = (provider: Resource, displayName: string) => {
    const providerDocument = JSON.parse(
      new TextDecoder().decode(provider.documentJson),
    ) as Record<string, unknown>;
    const protocol =
      providerDocument.protocol === "anthropic-messages"
        ? ApiProtocol.ANTHROPIC_MESSAGES
        : providerDocument.protocol === "openai-chat"
          ? ApiProtocol.OPENAI_CHAT
          : ApiProtocol.OPENAI_RESPONSES;
    return create(ProviderInventoryEntrySchema, {
      providerId: provider.id,
      displayName,
      enabled: true,
      provider,
      accountCountsAvailable: true,
      apiFormats: [
        create(ProviderApiFormatSchema, {
          protocol,
          endpoint: String(providerDocument.endpoint ?? ""),
          authentication: ApiAuthentication.BEARER,
        }),
      ],
    });
  };
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
    ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
    ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
  ];
  const requests: {
    query: string;
    enabledOnly: boolean;
    pageSize: number;
    pageToken: string;
  }[] = [];
  let failLater = true;
  const value = fixture([openai, anthropic, custom], {
    readProviderInventory: (pageToken, request) => {
      requests.push({ ...request, pageToken });
      if (!request.enabledOnly)
        return { entries: [entry(anthropic, "Anthropic")], capabilities };
      if (!pageToken)
        return {
          entries: [entry(openai, "OpenAI")],
          capabilities,
          nextPageToken: "enabled-page-2",
        };
      if (failLater) {
        failLater = false;
        throw new ConnectError("Temporary fixture failure", Code.Unavailable);
      }
      return { entries: [entry(custom, "Custom API")], capabilities };
    },
  });
  const savedAccount = resource(
    EntityKind.ACCOUNT,
    {
      alias: "Custom key",
      provider_id: custom.id,
      type: "api",
      api_protocol: "openai-chat",
      enabled: true,
      health: "disconnected",
    },
    4n,
  );
  value.save.mockImplementation(async (request: unknown) => ({
    resource: savedAccount,
    requestId: input(request).mutation.requestId,
  }));
  value.connect.mockImplementation(async (request: unknown) => ({
    account: create(ResourceSchema, {
      ...savedAccount,
      revision: 5n,
      documentJson: encode({
        alias: "Custom key",
        provider_id: custom.id,
        type: "api",
        api_protocol: "openai-chat",
        enabled: true,
        health: "unverified",
        connection: { id: newRequestId() },
      }),
    }),
    requestId: input(request).mutation.requestId,
  }));
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  await waitFor(() =>
    expect(
      requests.some((request) => !request.enabledOnly && request.query === ""),
    ).toBe(true),
  );
  expect(
    screen.queryByRole("searchbox", { name: "Search providers" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  expect(
    await screen.findByRole("button", { name: "OpenAI API key" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("searchbox", { name: "Search providers" }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Load more Provider pages" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Retry providers" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Custom API API key" }),
  );
  expect(screen.getByRole("heading", { name: "Connect your entry" })).toBe(
    window.document.activeElement,
  );
  expect(screen.getByText("Custom API", { selector: "strong" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  expect(screen.getByRole("button", { name: "Custom API API key" })).toBe(
    window.document.activeElement,
  );
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Load more Provider pages" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Back to AI API Keys" }));
  expect(
    screen.queryByRole("searchbox", { name: "Search providers" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add AI API key" }));
  fireEvent.click(screen.getByRole("button", { name: "Custom API API key" }));
  await waitFor(() =>
    expect(screen.getByLabelText("API format").matches(":disabled")).toBe(
      false,
    ),
  );
  fireEvent.change(screen.getByLabelText("API format"), {
    target: { value: "openai-chat" },
  });
  fireEvent.change(screen.getByLabelText("Entry name"), {
    target: { value: "Custom key" },
  });
  fireEvent.change(screen.getByLabelText("API key"), {
    target: { value: "fixture-only-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add and connect" }));
  await screen.findByRole("heading", { name: "Custom key" });
  await waitFor(() => expect(value.connect).toHaveBeenCalledTimes(1));
  expect(
    JSON.parse(
      new TextDecoder().decode(input(value.save.mock.calls[0][0]).documentJson),
    ).provider_id,
  ).toBe(custom.id);
  const choices = requests.filter((request) => request.enabledOnly);
  expect(
    choices.every((request) => request.query === "" && request.pageSize === 50),
  ).toBe(true);
  expect(
    choices.filter((request) => request.pageToken === "enabled-page-2"),
  ).toHaveLength(2);
});

it.each([
  ProviderInventoryCapability.PROVIDER_ACTIVATION,
  ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
  ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
  ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
])("requires every picker inventory capability (%s)", async (missing) => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "OpenAI",
    authentication: "bearer",
    protocol: "openai-chat",
    endpoint: "https://api.example.test/v1",
    enabled: true,
  });
  const entry = create(ProviderInventoryEntrySchema, {
    providerId: provider.id,
    displayName: "OpenAI",
    enabled: true,
    provider,
    accountCountsAvailable: true,
  });
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
    ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
    ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
  ];
  const value = fixture([provider], {
    readProviderInventory: (_, request) => ({
      entries: [entry],
      capabilities: request.enabledOnly
        ? capabilities.filter((capability) => capability !== missing)
        : capabilities,
    }),
  });
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  const add = screen.getByRole("button", { name: "Add AI API key" });
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(add);
  expect(
    await screen.findByText(/Provider choices are unavailable/),
  ).toBeTruthy();
  expect(screen.queryByRole("button", { name: "OpenAI API key" })).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("scopes shared preference and deletion terminology to API documents and preserves subscription aliases", async () => {
  for (const type of ["api", "subscription"] as const) {
    const account = resource(
      EntityKind.ACCOUNT,
      {
        alias: "Original API account alias",
        type,
        ...(type === "api"
          ? { provider_id: newRequestId() }
          : { subscription_service: "chatgpt" }),
        enabled: true,
        health: "disconnected",
      },
      7n,
    );
    const value = fixture([account]);
    const view = render(
      value.view(
        <ConfigurationEditor
          kind={EntityKind.ACCOUNT}
          initial={account}
          active
          saved={() => {}}
          cancel={() => {}}
        />,
      ),
    );
    expect(
      screen.getByRole("heading", {
        name: type === "api" ? "Edit preferences" : "Edit AI account",
      }),
    ).toBeTruthy();
    expect(
      (
        screen.getByLabelText(
          type === "api" ? "Entry name" : "Account alias",
        ) as HTMLInputElement
      ).value,
    ).toBe("Original API account alias");
    expect(
      screen.getByRole("checkbox", {
        name: type === "api" ? "Enable this entry" : "Enable this account",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("checkbox", {
        name:
          type === "api"
            ? "Exclude from automatic entry selection"
            : "Exclude from automatic account selection",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("checkbox", {
        name:
          type === "api"
            ? "Notify when entry quota recovers"
            : "Notify when account quota recovers",
      }),
    ).toBeTruthy();
    view.rerender(
      value.view(
        <ConfigurationDeletion
          initial={account}
          deleted={() => {}}
          close={() => {}}
        />,
      ),
    );
    expect(
      screen.getByRole("heading", {
        name: "Delete Original API account alias?",
      }),
    ).toBeTruthy();
    expect(
      screen.getByText(
        type === "api"
          ? "This disconnects the entry, removes its protected credentials, and deletes its saved configuration."
          : "This logs out the account and removes its protected credentials before deleting its saved configuration.",
      ),
    ).toBeTruthy();
    view.unmount();
    value.client.clear();
  }
});

it("keeps API connection actions separate from validation and preserves server-owned diagnostic wording", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "Hosted provider",
    authentication: "bearer",
    enabled: true,
  });
  const account = resource(
    EntityKind.ACCOUNT,
    {
      alias: "API account alias",
      provider_id: provider.id,
      type: "api",
      enabled: true,
      health: "unverified",
      connection: { id: newRequestId(), authentication: "bearer" },
      validation: {
        state: "failed",
        problem: {
          message: "Server account diagnostic",
          guidance: "Original account guidance",
        },
      },
    },
    5n,
  );
  const value = fixture([provider, account]);
  const view = render(
    value.view(<AccountConnection initial={account} active close={() => {}} />),
  );
  await screen.findByText(/Hosted provider/);
  expect(
    screen.getByRole("heading", { name: "Manage connection" }),
  ).toBeTruthy();
  expect(screen.getByText("API account alias")).toBeTruthy();
  expect(
    screen.getByText("Server account diagnostic Original account guidance"),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "Validate connection" }),
  ).toBeTruthy();
  expect(value.connect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Confirm disconnection" }),
  );
  await waitFor(() => expect(value.disconnect).toHaveBeenCalledTimes(1));
  expect(value.disconnect.mock.calls[0][0]).toMatchObject({
    mutation: { id: account.id, expectedRevision: 5n },
  });
  view.unmount();
  value.client.clear();
  const subscription = resource(EntityKind.ACCOUNT, {
    alias: "Subscription alias",
    subscription_service: "claude",
    type: "subscription",
    health: "disconnected",
  });
  render(
    value.view(
      <AccountConnection initial={subscription} active close={() => {}} />,
    ),
  );
  expect(
    screen.getByRole("button", { name: "Back to subscriptions" }),
  ).toBeTruthy();
  expect(
    await screen.findByText(
      /This server or subscription service does not support the requested lifecycle action/,
    ),
  ).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Connect API key" })).toBeNull();
  expect(screen.queryByLabelText("API key")).toBeNull();
});

it("keeps Subscription free of Provider requests while API inventory preserves exact search scope", async () => {
  const provider = resource(EntityKind.PROVIDER, {
    name: "Exact provider",
    endpoint: "https://api.example.test/v1",
    protocol: "openai-responses",
    authentication: "bearer",
    enabled: true,
  });
  const entry = create(ProviderInventoryEntrySchema, {
    providerId: provider.id,
    displayName: "Exact provider",
    enabled: true,
    provider,
    totalAccounts: 1n,
    accountCountsAvailable: true,
  });
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
    ProviderInventoryCapability.ACCOUNT_API_PROTOCOL_V1,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
    ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
  ];
  const requests: {
    query: string;
    pageToken: string;
    enabledOnly: boolean;
    pageSize: number;
  }[] = [];
  const value = fixture([provider], {
    readResources: (kind, token) => ({
      resources: [],
      nextPageToken: kind === EntityKind.ACCOUNT && !token ? "api-page-2" : "",
    }),
    readProviderInventory: (pageToken, request) => {
      requests.push({ ...request, pageToken });
      return {
        entries: [entry],
        capabilities,
        nextPageToken:
          request.query === "Exact" && !pageToken ? "provider-page-2" : "",
      };
    },
  });
  render(value.view(<Settings />));
  expect(screen.queryByText("Advanced settings")).toBeNull();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Load more Subscription account pages",
    }),
  );
  await screen.findByRole("heading", { name: "No subscriptions yet" });
  expect(requests).toHaveLength(0);
  expect(screen.queryByLabelText("Search providers")).toBeNull();
  const apiStart = requests.length;
  fireEvent.click(screen.getByRole("button", { name: "AI API Keys" }));
  await screen.findByText("No entries on this page.");
  expect(
    screen.queryByRole("searchbox", { name: "Search providers" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Clear provider filter" }),
  ).toBeNull();
  expect(
    requests
      .slice(apiStart)
      .every((request) => request.query === "" && request.pageSize === 50),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  fireEvent.change(screen.getByLabelText("Search API providers"), {
    target: { value: "Exact" },
  });
  await waitFor(() =>
    expect(requests.some((request) => request.query === "Exact")).toBe(true),
  );
  const moreProviders = await screen.findByRole("button", {
    name: "Load more Provider pages",
  });
  await waitFor(() =>
    expect((moreProviders as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(moreProviders);
  await waitFor(() =>
    expect(
      requests.some(
        (request) =>
          request.query === "Exact" && request.pageToken === "provider-page-2",
      ),
    ).toBe(true),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Manage AI API Keys" }),
  );
  await screen.findByText("Provider: Exact provider");
  const accountRead = () =>
    value.list.mock.calls
      .map(([request]) => request)
      .filter((request) => request.filter?.kind === EntityKind.ACCOUNT)
      .at(-1);
  await waitFor(() =>
    expect(accountRead()).toMatchObject({
      providerId: provider.id,
      accountType: 1,
      filter: { pageToken: "", pageSize: 50 },
    }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Load more Entry pages" }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Load more Entry pages" }),
    ).toBeNull(),
  );
  expect(accountRead()).toMatchObject({
    providerId: provider.id,
    filter: { pageToken: "api-page-2" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Clear provider filter" }),
  );
  await waitFor(() =>
    expect(accountRead()).toMatchObject({
      providerId: "",
      filter: { pageToken: "" },
    }),
  );
  expect(screen.queryByRole("button", { name: "First page" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "AI Subscription" }));
  expect(screen.queryByLabelText("Search providers")).toBeNull();
  expect(screen.queryByLabelText("Filter accounts by provider")).toBeNull();
  expect(screen.queryByText("Advanced settings")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  expect(
    (screen.getByLabelText("Search API providers") as HTMLInputElement).value,
  ).toBe("");
  await waitFor(() =>
    expect(
      requests.filter((request) => !request.enabledOnly).at(-1),
    ).toMatchObject({ query: "", pageToken: "" }),
  );
  expect(value.save).not.toHaveBeenCalled();
  expect(value.connect).not.toHaveBeenCalled();
});

it("scopes Transfer presentation and discards it on category departure", async () => {
  const value = fixture([]);
  const rendered = render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const heading = screen.getByRole("heading", {
    name: "Import / Export",
    level: 1,
  });
  expect(heading.closest(".settings-transfer-column")).toBeTruthy();
  expect(heading.getAttribute("aria-live")).toBe("polite");
  expect(
    screen.getByText("Move configuration between DeliDev servers."),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  expect(
    screen
      .getByRole("heading", { name: "Instructions", level: 1 })
      .closest(".settings-transfer-column"),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  const json = screen.getByRole("textbox", {
    name: "Configuration JSON",
  }) as HTMLTextAreaElement;
  const raw = '{"version":1,"entries":[],"machines":[]}';
  fireEvent.change(json, { target: { value: raw } });
  for (const button of within(
    screen.getByRole("navigation", { name: "Settings categories" }),
  ).getAllByRole("button"))
    expect((button as HTMLButtonElement).disabled).toBe(false);
  expect(
    screen.queryByRole("combobox", { name: "Settings category" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Load configuration document" }),
  );
  rendered.rerender(value.view(<Settings visible />));
  expect(screen.getByRole("textbox", { name: "Configuration JSON" })).toBe(
    json,
  );
  expect(json.value).toBe(raw);
  fireEvent.keyDown(json, { key: "Escape" });
  expect(screen.getByRole("textbox", { name: "Configuration JSON" })).toBe(
    json,
  );
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  fireEvent.click(screen.getByRole("button", { name: "Import / Export" }));
  expect(
    (
      screen.getByRole("textbox", {
        name: "Configuration JSON",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("");
  expect(
    screen.queryByRole("button", { name: "Preview configuration changes" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", {
      name: "Retry the same configuration import",
    }),
  ).toBeNull();
  for (const button of within(
    screen.getByRole("navigation", { name: "Settings categories" }),
  ).getAllByRole("button"))
    expect((button as HTMLButtonElement).disabled).toBe(false);
});

it("keeps repository source metadata inert and preserves schema-bound actions", () => {
  const row = resource(EntityKind.REPOSITORY, {
    name: "Remote repository",
    remote_url: "https://user:private-value@example.invalid/repo.git",
    checkouts: [{ machine_id: newRequestId(), path: "/saved/checkout" }],
    github_owner: "example",
    github_name: "repo",
    integration_id: newRequestId(),
  });
  const value = fixture([row]),
    edit = vi.fn(),
    remove = vi.fn();
  const absentBrowser = () => {
    expect(
      screen.queryByRole("button", { name: "Browse GitHub items" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Close GitHub items" }),
    ).toBeNull();
    expect(
      screen.queryByRole("region", { name: "Browse repository GitHub items" }),
    ).toBeNull();
    expect(document.querySelector(".repository-github-tools")).toBeNull();
  };
  const view = render(
    value.view(<RepositoryRow row={row} edit={edit} remove={remove} />),
  );
  expect(screen.getByText("Git remote repository")).toBeTruthy();
  expect(screen.getByText("GitHub configured")).toBeTruthy();
  expect(
    screen.queryByText(
      "Saved settings only. Inspect access to check permissions.",
    ),
  ).toBeNull();
  expect(screen.getByText("/saved/checkout")).toBeTruthy();
  expect(screen.queryByText(row.id)).toBeNull();
  expect(view.container.textContent).not.toContain("private-value");
  expect(view.container.textContent).not.toContain("example.invalid");
  expect(
    (
      screen.getByRole("button", {
        name: "Edit Remote repository",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "Edit Remote repository" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Delete Remote repository" }),
  );
  expect(edit).toHaveBeenCalledTimes(1);
  expect(remove).toHaveBeenCalledTimes(1);
  expect(
    (
      screen.getByRole("button", {
        name: "Delete Remote repository",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  expect(
    screen.queryByRole("button", { name: "Inspect GitHub access" }),
  ).toBeNull();
  absentBrowser();
  expect(value.list).not.toHaveBeenCalled();
  expect(value.inspect).not.toHaveBeenCalled();
  expect(value.save).not.toHaveBeenCalled();
  view.rerender(
    value.view(
      <RepositoryRow
        row={create(ResourceSchema, { ...row, schemaVersion: 99 })}
        edit={edit}
        remove={remove}
      />,
    ),
  );
  expect(screen.getByText("Unsupported format")).toBeTruthy();
  absentBrowser();
  expect(
    screen
      .getAllByRole("button")
      .every((button) => (button as HTMLButtonElement).disabled),
  ).toBe(true);
  expect(screen.queryByText("/saved/checkout")).toBeNull();
  expect(screen.queryByText(row.id)).toBeNull();
  view.rerender(
    value.view(
      <RepositoryRow
        row={resource(EntityKind.REPOSITORY, {
          name: "Unconfigured repository",
        })}
        edit={edit}
        remove={remove}
      />,
    ),
  );
  expect(screen.getByText("GitHub not configured")).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Inspect GitHub access" }),
  ).toBeNull();
  absentBrowser();
});

it("keeps repository continuation empties and failed refreshes distinct from a final empty first page", async () => {
  let fail = false;
  const value = fixture([], {
    readResources: (kind, token) => {
      if (kind !== EntityKind.REPOSITORY) return { resources: [] };
      if (fail)
        throw new ConnectError("Synthetic read failure", Code.PermissionDenied);
      return { resources: [], nextPageToken: token ? "" : "repository-page-2" };
    },
  });
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Repositories" }));
  await screen.findByText("No repositories on this page.");
  expect(
    screen.queryByRole("region", { name: "No repositories yet" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Load more Settings pages" }),
  );
  await screen.findByRole("region", { name: "No repositories yet" });
  fail = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
  await screen.findByText(
    "Refresh failed. Showing the last successfully loaded results.",
  );
  expect(
    screen.queryByRole("region", { name: "No repositories yet" }),
  ).toBeNull();
  expect(screen.queryByText("No repositories on this page.")).toBeNull();
  expect(screen.queryByText("No saved entries.")).toBeNull();
});

// Localization changes presentation without disposing the original read-only task.
it("keeps the routing dialog and its read when the Settings language changes", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Luna MAX" }),
    account = resource(EntityKind.ACCOUNT, {
      alias: "ChatGPT Personal",
      type: "subscription",
      subscription_service: "chatgpt",
    });
  const value = fixture([agent, account]);
  value.preview.mockResolvedValue({
    routeJson: encode({
      policy: "priority",
      candidates: [
        {
          id: account.id,
          weight: 1,
          eligibility: "unauthenticated",
          quota_state: "unknown",
        },
      ],
    }),
  });
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Preview routing for Luna MAX" }),
  );
  await screen.findByText("ChatGPT Personal");
  const dialog = screen.getByRole("dialog");
  const { i18n, SupportedLanguage } = await import("./localization");
  await act(async () => {
    await i18n.changeLanguage(SupportedLanguage.Korean);
  });
  expect(screen.getByRole("dialog")).toBe(dialog);
  expect(screen.getByText("후보 계정")).toBeTruthy();
  expect(value.preview).toHaveBeenCalledTimes(1);
});

it("does not read policy inventory while inline Network settings starts expanded", async () => {
  const preferences = resource(EntityKind.SETTINGS, {
    default_routing: "priority",
    automatic_fetch: true,
    notifications: false,
    remediation: {
      ci_failure: true,
      review_feedback: false,
      merge_conflict: true,
      conflict_strategy: "rebase",
      session_strategy: "dedicated",
      attempt_limit: 9,
      agent_id: newRequestId(),
      machine_id: newRequestId(),
    },
  });
  const value = fixture([preferences]);
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Server preferences" }));
  expect(screen.queryByRole("form")).toBeNull();
  expect(
    screen
      .getByRole("button", { name: "Network settings" })
      .getAttribute("aria-expanded"),
  ).toBe("true");
  expect(screen.queryByRole("dialog", { name: "Server network" })).toBeNull();
  const reads = value.list.mock.calls.filter(
    ([request]) => request.filter?.kind === EntityKind.SETTINGS,
  ).length;
  await act(async () => {
    await value.client.invalidateQueries();
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(
    value.list.mock.calls.filter(
      ([request]) => request.filter?.kind === EntityKind.SETTINGS,
    ),
  ).toHaveLength(reads);
  expect(screen.queryByRole("form")).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});

it.each(["queued", "succeeded", "failed", "unknown"])(
  "retains selected-server API entry scope through an asynchronous %s save",
  async (state) => {
    const account = resource(EntityKind.ACCOUNT, {
      type: "api",
      alias: "Scoped API entry",
      provider_id: newRequestId(),
      enabled: true,
      health: "disconnected",
    });
    const job = resource(EntityKind.JOB, { type: "save-account", state });
    const value = fixture([account, job]);
    value.save.mockResolvedValueOnce(state === "unknown" ? {} : { job });
    render(
      value.view(
        <ConfigurationEditor
          kind={EntityKind.ACCOUNT}
          initial={account}
          active
          saved={vi.fn()}
          cancel={vi.fn()}
        />,
      ),
    );
    const save = screen.getByRole("button", { name: "Save AI API key entry" });
    await waitFor(() =>
      expect((save as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(save);
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Save AI API key entry" }),
      ).toBeNull(),
    );
    expect(screen.getByText("Saved on the selected server.")).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "Edit preferences" }),
    ).toBeTruthy();
    expect(screen.getByText("Scoped API entry")).toBeTruthy();
  },
);

it.each([true, false])(
  "blocks configuration submit until an exact picker read accepts the choice while preserving sibling edits (prior selection: %s)",
  async (hasPrior) => {
    const oldProfile = resource(EntityKind.INTEGRATION, {
      name: "Previous profile",
    });
    const newProfile = resource(EntityKind.INTEGRATION, {
      name: "Chosen profile",
    });
    const repository = resource(EntityKind.REPOSITORY, {
      name: "Repository",
      integration_id: hasPrior ? oldProfile.id : "",
      checkouts: [],
    });
    let resolve!: (value: { resource: Resource }) => void;
    const pending = new Promise<{ resource: Resource }>((done) => {
      resolve = done;
    });
    const value = fixture([repository, oldProfile, newProfile], {
      readResource: (id) =>
        id === newProfile.id
          ? pending
          : { resource: [repository, oldProfile].find((row) => row.id === id) },
    });
    render(
      value.view(
        <ConfigurationEditor
          kind={EntityKind.REPOSITORY}
          initial={repository}
          active
          saved={() => {}}
          cancel={() => {}}
        />,
      ),
    );
    const picker = screen.getByRole("combobox", { name: "GitHub profile" });
    fireEvent.click(picker);
    fireEvent.click(
      await screen.findByRole("option", { name: "Chosen profile" }),
    );
    const save = screen.getByRole("button", { name: "Save Repository" });
    const form = save.closest("form")!;
    fireEvent.submit(form);
    expect(value.save).not.toHaveBeenCalled();
    expect((save as HTMLButtonElement).disabled).toBe(true);
    expect(picker.dataset.value).toBe(hasPrior ? oldProfile.id : "");
    fireEvent.change(screen.getByRole("textbox", { name: "Name" }), {
      target: { value: "Edited while reading" },
    });
    fireEvent.submit(form);
    expect(value.save).not.toHaveBeenCalled();
    await act(async () => {
      resolve({ resource: newProfile });
      await pending;
      fireEvent.submit(form);
      expect(value.save).not.toHaveBeenCalled();
    });
    await waitFor(() => expect(picker.dataset.value).toBe(newProfile.id));
    fireEvent.submit(form);
    await waitFor(() => expect(value.save).toHaveBeenCalledTimes(1));
    expect(
      JSON.parse(
        new TextDecoder().decode(
          input(value.save.mock.calls[0][0]).documentJson,
        ),
      ),
    ).toMatchObject({
      name: "Edited while reading",
      integration_id: newProfile.id,
    });
  },
);

it("does not treat a pending or failed subscription support read as proved unsupported", async () => {
  const account = resource(EntityKind.ACCOUNT, {
    alias: "Original subscription",
    subscription_service: "claude",
    type: "subscription",
    health: "disconnected",
  });
  const value = fixture([account], {
    systemStatusError: new ConnectError(
      "Fixture status failure",
      Code.PermissionDenied,
    ),
  });
  render(
    value.view(<AccountConnection initial={account} active close={() => {}} />),
  );
  expect(
    screen.getByText(
      "Reading the selected server’s subscription support. This read does not start a login.",
    ),
  ).toBeTruthy();
  expect(
    screen.queryByText(
      /This server or subscription service does not support the requested lifecycle action/,
    ),
  ).toBeNull();
  await screen.findByText(
    "This connection is not authorized for the action. Check its access.",
  );
  expect(
    screen.queryByText(
      /This server or subscription service does not support the requested lifecycle action/,
    ),
  ).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: "Sign in to Claude",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.connect).not.toHaveBeenCalled();
  expect(value.disconnect).not.toHaveBeenCalled();
});

it.each(["cancel", "close", "done"])(
  "retains original inline model identities through %s and a failed inventory refresh",
  async (exit) => {
    const log = vi.spyOn(console, "warn").mockImplementation(() => {});
    const providerId = newRequestId(),
      accountId = newRequestId();
    const route = (nativeId: string) => [
      {
        model: { provider_id: providerId, native_id: nativeId },
        accounts: [{ id: accountId, weight: 1 }],
      },
    ];
    const first = resource(EntityKind.AGENT, {
      name: "First retained Worker",
      harness: "codex",
      routes: route("original-inline-native"),
    });
    const second = resource(EntityKind.AGENT, {
      name: "Second retained Worker",
      harness: "codex",
      routes: route("original-inline-native"),
    });
    let visible = [first, second],
      failedInventory = false;
    const modelReads = vi.fn(() => {
      throw new ConnectError(
        "Retired registry must not be read",
        Code.PermissionDenied,
      );
    });
    const value = fixture([first, second], {
      readResource: (id) => ({
        resource: visible.find((row) => row.id === id),
      }),
      readResources: (kind) => {
        if (kind === EntityKind.MODEL) return modelReads();
        if (kind === EntityKind.AGENT && failedInventory)
          throw new ConnectError(
            "Synthetic inventory refresh failure",
            Code.Unavailable,
          );
        return { resources: kind === EntityKind.AGENT ? visible : [] };
      },
    });
    value.remove.mockImplementation(async () => {
      visible = [second];
      return {};
    });
    render(value.view(<Settings />));
    fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
    await screen.findAllByText("original-inline-native");
    expect(modelReads).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", {
        name: `Delete First retained Worker`,
      }),
    );
    if (exit === "done")
      fireEvent.click(
        screen.getByRole("button", { name: "Confirm configuration deletion" }),
      );
    else {
      if (exit === "cancel")
        fireEvent(
          screen.getByRole("dialog"),
          new Event("cancel", { cancelable: true }),
        );
      else
        fireEvent.click(
          screen.getByRole("button", { name: "Close Delete configuration" }),
        );
    }
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await screen.findByRole("heading", { name: "Second retained Worker" });
    if (exit === "done") expect(value.remove).toHaveBeenCalledTimes(1);
    else expect(value.remove).not.toHaveBeenCalled();
    visible = visible.map((row) =>
      create(ResourceSchema, {
        ...row,
        revision: row.revision + 1n,
        documentJson: encode({
          ...JSON.parse(new TextDecoder().decode(row.documentJson)),
          routes: route("explicit-refresh-native"),
        }),
      }),
    );
    failedInventory = true;
    fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
    await screen.findByText(
      "Refresh failed. Showing the last successfully loaded results.",
    );
    expect(
      screen.getAllByText("original-inline-native").length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText("explicit-refresh-native")).toBeNull();
    expect(modelReads).not.toHaveBeenCalled();
    failedInventory = false;
    fireEvent.click(screen.getByRole("button", { name: "Refresh settings" }));
    await screen.findAllByText("explicit-refresh-native");
    expect(modelReads).not.toHaveBeenCalled();
    expect(screen.queryByText("original-inline-native")).toBeNull();
    log.mockRestore();
  },
);

it("retains Connection controls without mounting or reading Settings Doctor", async () => {
  const doctor = vi.fn(() => ({}));
  const value = fixture([], { doctor });
  render(
    value.view(
      <Settings
        connectionSettings={<button>Original native connection</button>}
      />,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Connections" }));
  await screen.findByRole("button", { name: "Original native connection" });
  expect(screen.getByRole("heading", { name: "Connections" })).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Refresh diagnostics" }),
  ).toBeNull();
  expect(screen.queryByText("Reading server diagnostics")).toBeNull();
  expect(doctor).not.toHaveBeenCalled();
});

it("Connections renders no App information shortcut and starts with the connection controls", async () => {
  const value = fixture([]);
  render(
    value.view(
      <Settings
        connectionSettings={
          <section aria-label="Original native connection">
            <h2>Original native connection</h2>
          </section>
        }
      />,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Connections" }));
  const connection = await screen.findByRole("region", {
    name: "Original native connection",
  });
  expect(
    screen.queryByRole("button", { name: "Open App information" }),
  ).toBeNull();
  expect(screen.queryByText("Open App information")).toBeNull();
  const panels = connection.closest(".settings-panels");
  expect(panels).not.toBeNull();
  expect(panels?.querySelector("button")).toBeNull();
  expect(panels?.firstElementChild?.firstElementChild).toBe(connection);
});

it("Connections falls back to the Current connection section without an App information shortcut", async () => {
  const value = fixture([]);
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Connections" }));
  const current = await screen.findByRole("region", {
    name: "Current connection",
  });
  expect(
    screen.queryByRole("button", { name: "Open App information" }),
  ).toBeNull();
  const panels = current.closest(".settings-panels");
  expect(panels?.firstElementChild?.firstElementChild).toBe(current);
  expect(panels?.querySelector("button")).toBeNull();
});

it("Korean Connections has no App information shortcut while the sidebar entry remains", async () => {
  const value = fixture([]);
  render(value.view(<Settings />));
  try {
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.Korean);
    });
    fireEvent.click(screen.getByRole("button", { name: "연결" }));
    expect(screen.queryByRole("button", { name: "앱 정보 열기" })).toBeNull();
    expect(screen.queryByText("앱 정보 열기")).toBeNull();
    expect(screen.getByRole("button", { name: "앱 정보" })).toBeTruthy();
  } finally {
    await act(async () => {
      await i18n.changeLanguage(SupportedLanguage.English);
    });
  }
});

it("App information stays reachable from its sidebar category", async () => {
  const value = fixture([]);
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "App information" }));
  expect(
    await screen.findByRole("heading", { name: "App information", level: 1 }),
  ).toBeTruthy();
});

it("Settings search keeps the active draft on typing and same-category selection, and discards query on departure", async () => {
  const value = fixture([]);
  const view = render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Project defaults" }));
  const attempts = await screen.findByRole("spinbutton", {
    name: "Consecutive automatic attempt limit",
  });
  fireEvent.change(attempts, { target: { value: "8" } });
  const input = screen.getByRole("searchbox");
  fireEvent.change(input, { target: { value: "attempt limit" } });
  expect(
    screen.getByRole("spinbutton", {
      name: "Consecutive automatic attempt limit",
    }),
  ).toBe(attempts);
  expect((attempts as HTMLInputElement).value).toBe("8");
  fireEvent.click(
    screen.getByRole("button", {
      name: "Project defaults › Consecutive automatic attempt limit",
    }),
  );
  await waitFor(() =>
    expect(
      document.activeElement?.getAttribute("data-settings-search-target"),
    ).toBe("remediation-attempts"),
  );
  expect(
    screen.getByRole("spinbutton", {
      name: "Consecutive automatic attempt limit",
    }),
  ).toBe(attempts);
  expect((attempts as HTMLInputElement).value).toBe("8");
  expect(value.save).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.inspect).not.toHaveBeenCalled();
  view.rerender(value.view(<Settings visible={false} />));
  view.rerender(value.view(<Settings />));
  expect((screen.getByRole("searchbox") as HTMLInputElement).value).toBe("");
  expect(
    screen.getByRole("heading", { level: 1, name: "AI Subscription" }),
  ).toBeTruthy();
});
it("Settings search focuses Network and SSH entry sections without opening their workflows", async () => {
  const value = fixture([]);
  render(value.view(<Settings />));
  const input = screen.getByRole("searchbox");
  fireEvent.change(input, { target: { value: "Network settings" } });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Server preferences › Network settings",
    }),
  );
  await waitFor(() =>
    expect(
      document.activeElement?.getAttribute("data-settings-search-target"),
    ).toBe("network"),
  );
  expect(
    screen.queryByRole("combobox", { name: "Outbound profile" }),
  ).toBeNull();
  fireEvent.change(input, { target: { value: "SSH" } });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Runner Devices › Set up a Worker over SSH",
    }),
  );
  await waitFor(() =>
    expect(
      document.activeElement?.getAttribute("data-settings-search-target"),
    ).toBe("ssh"),
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
  expect(value.inspect).not.toHaveBeenCalled();
});
it("Settings search respects malformed singleton admission and never starts a write", async () => {
  const value = fixture([
    {
      ...resource(EntityKind.SETTINGS, {
        default_routing: "remaining-quota",
        remediation: {},
      }),
      schemaVersion: 99,
    },
  ]);
  render(value.view(<Settings />));
  fireEvent.change(screen.getByRole("searchbox"), {
    target: { value: "account routing" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Project defaults › Account routing" }),
  );
  await screen.findByText("This setting is unavailable here.");
  expect(document.activeElement).toBe(
    screen.getByRole("heading", { level: 1, name: "Project defaults" }),
  );
  expect(
    screen.queryByRole("combobox", { name: "Default account routing" }),
  ).toBeNull();
  expect(value.save).not.toHaveBeenCalled();
});
it("Settings search preserves the original uncertain server-preference request until explicit retry", async () => {
  const value = fixture([]);
  value.save.mockRejectedValueOnce(
    new ConnectError("Original unavailable save", Code.Unavailable),
  );
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Project defaults" }));
  const fetch = await screen.findByRole("checkbox", {
    name: "Allow automatic fetch before Worktree preparation",
  });
  fireEvent.click(fetch);
  const submittedFetch = (fetch as HTMLInputElement).checked;
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  const retry = await screen.findByRole("button", {
    name: "Retry the same configuration",
  });
  const original = input(value.save.mock.calls[0][0]);
  const search = screen.getByRole("searchbox");
  fireEvent.change(search, { target: { value: "automatic fetch" } });
  fireEvent.change(search, { target: { value: "" } });
  fireEvent.change(search, { target: { value: "automatic fetch" } });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Project defaults › Allow automatic fetch before Worktree preparation",
    }),
  );
  await waitFor(() =>
    expect(
      document.activeElement?.getAttribute("data-settings-search-target"),
    ).toBe("automatic-fetch"),
  );
  expect(
    screen.getByRole("button", { name: "Retry the same configuration" }),
  ).toBe(retry);
  expect((fetch as HTMLInputElement).checked).toBe(submittedFetch);
  expect(value.save).toHaveBeenCalledTimes(1);
  fireEvent.click(retry);
  await waitFor(() => expect(value.save).toHaveBeenCalledTimes(2));
  const retried = input(value.save.mock.calls[1][0]);
  expect(retried.mutation.requestId).toBe(original.mutation.requestId);
  expect(retried.documentJson).toEqual(original.documentJson);
});

it("keeps confirmed provider switches and layout content through delayed off/on and manual refresh", async () => {
  let provider = resource(EntityKind.PROVIDER, {
    name: "OpenAI",
    endpoint: "https://api.openai.com/v1",
    protocol: "openai-responses",
    authentication: "bearer",
    enabled: true,
    preset_id: "openai",
  });
  const other = resource(EntityKind.PROVIDER, {
    name: "Anthropic",
    endpoint: "https://api.anthropic.com",
    protocol: "anthropic",
    authentication: "bearer",
    enabled: true,
    preset_id: "anthropic",
  });
  const entry = () =>
    create(ProviderInventoryEntrySchema, {
      presetId: ProviderPresetId.OPENAI,
      providerId: provider.id,
      displayName: "OpenAI",
      enabled: JSON.parse(new TextDecoder().decode(provider.documentJson))
        .enabled,
      provider,
      accountCountsAvailable: true,
    });
  const capabilities = [
    ProviderInventoryCapability.PROVIDER_ACTIVATION,
    ProviderInventoryCapability.ACTIVE_API_MODEL_FILTER,
    ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
  ];
  let hold = false,
    failRefresh = false;
  const pending: Array<() => void> = [];
  const value = fixture([provider, other], {
    readProviderInventory: (_token, request) => {
      if (failRefresh && !request.enabledOnly)
        throw new ConnectError(
          "Synthetic retained refresh failure",
          Code.Unavailable,
        );
      const response = {
        capabilities,
        entries: [
          entry(),
          create(ProviderInventoryEntrySchema, {
            presetId: ProviderPresetId.ANTHROPIC,
            providerId: other.id,
            displayName: "Anthropic",
            enabled: true,
            provider: other,
            accountCountsAvailable: true,
          }),
        ],
      };
      return hold && !request.enabledOnly
        ? new Promise((resolve) => pending.push(() => resolve(response)))
        : response;
    },
  });
  value.save.mockImplementation(async (request) => {
    provider = create(ResourceSchema, {
      ...provider,
      revision: provider.revision + 1n,
      documentJson: input(request).documentJson,
    });
    hold = true;
    return { resource: provider };
  });
  render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const original = await screen.findByRole("switch", {
    name: "Turn off OpenAI",
  });
  for (const enabled of [true, false]) {
    fireEvent.click(original);
    await waitFor(() => expect(pending.length).toBeGreaterThan(0));
    expect(original.getAttribute("aria-checked")).toBe(String(enabled));
    expect(screen.queryByText(/Refreshing provider state/)).toBeNull();
    hold = false;
    await act(async () => {
      pending.splice(0).forEach((resolve) => resolve());
    });
    await waitFor(() =>
      expect(original.getAttribute("aria-checked")).toBe(String(!enabled)),
    );
    expect(
      await screen.findByRole("switch", {
        name: `${enabled ? "Turn on" : "Turn off"} OpenAI`,
      }),
    ).toBe(original);
  }
  hold = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh providers" }));
  await waitFor(() => expect(pending.length).toBeGreaterThan(0));
  expect(screen.queryByText(/Refreshing provider state/)).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: "Refresh providers",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(original.getAttribute("aria-checked")).toBe("true");
  hold = false;
  await act(async () => {
    pending.splice(0).forEach((resolve) => resolve());
  });
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Refresh providers",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  failRefresh = true;
  fireEvent.click(screen.getByRole("button", { name: "Refresh providers" }));
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(original.getAttribute("aria-checked")).toBe("true");
  expect(screen.getByRole("switch", { name: "Turn off OpenAI" })).toBe(
    original,
  );
});

it.each(Object.values(SupportedLanguage))(
  "shows API provider guidance once with exact and unavailable counts in %s",
  async (language) => {
    const custom = resource(EntityKind.PROVIDER, {
      name: "Custom fixture",
      enabled: true,
    });
    const entries = [
      create(ProviderInventoryEntrySchema, {
        presetId: ProviderPresetId.OPENAI,
        displayName: "Hosted fixture",
        enabled: true,
        accountCountsAvailable: true,
      }),
      create(ProviderInventoryEntrySchema, {
        presetId: ProviderPresetId.OLLAMA,
        displayName: "Local fixture",
        enabled: true,
        connectedAccounts: 1n,
        totalAccounts: 2n,
        accountCountsAvailable: true,
      }),
      create(ProviderInventoryEntrySchema, {
        providerId: custom.id,
        provider: custom,
        displayName: "Custom fixture",
        enabled: true,
        accountCountsAvailable: false,
      }),
    ];
    const value = fixture([custom], { providerEntries: entries });
    render(value.view(<Settings visible />));
    fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
    await screen.findByText("Custom fixture", {
      selector: ".api-provider-name span",
    });
    await act(async () => {
      await i18n.changeLanguage(language);
    });
    const guidance = copy("provider-model-settings.connectionGuidance");
    expect(screen.getAllByText(guidance)).toHaveLength(1);
    const paragraph = screen.getByText(guidance),
      search = screen.getByRole("textbox", {
        name: copy("provider-model-settings.searchApiProviders_1b03d9"),
      });
    expect(
      paragraph.compareDocumentPosition(search) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(paragraph.closest("article")).toBeNull();
    for (const [name, count] of [
      [
        "Hosted fixture",
        copy("provider-model-settings.sentence.490d50c6611c", {
          v0: "0",
          v1: "0",
        }),
      ],
      [
        "Local fixture",
        copy("provider-model-settings.sentence.490d50c6611c", {
          v0: "1",
          v1: "2",
        }),
      ],
      ["Custom fixture", copy("provider-model-settings.extra.555765b26ebc")],
    ]) {
      const row = screen
        .getByText(name, { selector: ".api-provider-name span" })
        .closest("article")!;
      expect(row.textContent).toContain(count);
      expect(row.textContent).not.toContain(guidance);
    }
    fireEvent.change(search, { target: { value: "no-match" } });
    expect(screen.getAllByText(guidance)).toHaveLength(1);
    expect(value.connect).not.toHaveBeenCalled();
    expect(value.save).not.toHaveBeenCalled();
  },
);

it("reveals a hidden invalid Project branch prefix through complete-document Save validation", async () => {
  const repository = resource(EntityKind.REPOSITORY, { name: "Repository" }),
    project = resource(EntityKind.PROJECT, {
      name: "Project",
      repositories: [repository.id],
      primary_repository: repository.id,
      agents: { configured: false, ids: [] },
      accounts: { configured: false, ids: [] },
      settings: { branch_prefix: "delidev/", plan_mode_default: "inherit" },
    });
  const f = fixture([project, repository], {
    systemCapabilities: [
      SystemCapability.SESSION_DEFAULTS_V1,
      SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1,
    ],
  });
  render(
    f.view(
      <ConfigurationEditor
        kind={EntityKind.PROJECT}
        initial={project}
        active
        saved={() => {}}
        cancel={() => {}}
      />,
    ),
  );
  fireEvent.click(screen.getByRole("tab", { name: "Execution" }));
  const prefix = await screen.findByLabelText("Literal branch prefix");
  fireEvent.change(prefix, { target: { value: "bad..prefix" } });
  fireEvent.click(screen.getByRole("tab", { name: "General" }));
  const save = screen.getByRole("button", { name: "Save Project" });
  await waitFor(() => expect(save).toHaveProperty("disabled", false));
  fireEvent.click(save);
  await waitFor(() =>
    expect(
      screen
        .getByRole("tab", { name: "Execution" })
        .getAttribute("aria-selected"),
    ).toBe("true"),
  );
  await waitFor(() => expect(document.activeElement).toBe(prefix));
  expect(f.save).not.toHaveBeenCalled();
});

// Issue 3183: Agent Workers category and owned task windows play one Y4
// entrance each, and a task's step heading is not animated by its opening.
it("plays one entrance for the Agent Workers category and for each owned task window", async () => {
  const entranceSettled = (node: Element) => {
    const event = new Event("animationend", { bubbles: true });
    Object.defineProperty(event, "animationName", { value: "entrance-y4" });
    act(() => {
      node.dispatchEvent(event);
    });
  };
  const agent = resource(EntityKind.AGENT, { name: "Entrance Agent" });
  const value = fixture([agent]);
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  const column = document.querySelector<HTMLElement>(
    ".settings-content-column > div",
  )!;
  expect(column.getAttribute("data-entrance")).toBe("y4");
  await screen.findByRole("heading", { name: "Entrance Agent" });
  // Reads and list updates never replay the category entrance.
  entranceSettled(column);
  await act(async () => {
    await value.client.invalidateQueries();
  });
  expect(column.getAttribute("data-entrance")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Preview routing for Entrance Agent" }),
  );
  let header = document.querySelector<HTMLElement>(".settings-task-header")!;
  expect(header.getAttribute("data-entrance")).toBe("y4");
  expect(
    document
      .querySelector("dialog.settings-task-dialog")!
      .hasAttribute("data-entrance"),
  ).toBe(false);
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: /^Close / }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Edit Entrance Agent" }));
  header = document.querySelector<HTMLElement>(".settings-task-header")!;
  expect(header.getAttribute("data-entrance")).toBe("y4");
  // Opening the wizard task does not animate the step heading.
  const step = await screen.findByRole("heading", {
    name: "Harness",
    level: 3,
  });
  expect(step.getAttribute("data-entrance")).toBeNull();
  expect(column.getAttribute("data-entrance")).toBeNull();
});

it("opens a Settings category in a new window on a primary modified click and keeps the selected category here", async () => {
  const destination = fakeDestinationWindow();
  const value = fixture([]);
  render(destination.wrap(value.view(<Settings />)));
  const agents = screen.getByRole("button", { name: "Agent Workers" });
  const current = document.querySelector(
    ".settings-category-button[aria-current='page']",
  )!;
  fireEvent.click(agents, primaryClick);
  expect(destination.opened).toHaveBeenCalledExactlyOnceWith({
    surface: Surface.Settings,
    category: SettingsCategory.AgentWorkers,
  });
  expect(agents.getAttribute("aria-current")).toBeNull();
  expect(
    document.querySelector(".settings-category-button[aria-current='page']"),
  ).toBe(current);
  fireEvent.click(agents, { detail: 1 });
  expect(agents.getAttribute("aria-current")).toBe("page");
  expect(destination.opened).toHaveBeenCalledTimes(1);
});

it("marks the Agent Worker deletion opener, confirmation and retry Danger while Edit and Keep stay neutral", async () => {
  const agent = resource(EntityKind.AGENT, { name: "Toned Agent" }, 7n),
    value = fixture([agent]);
  value.remove.mockRejectedValueOnce(
    new ConnectError("Acknowledgment lost", Code.Unavailable),
  );
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Agent Workers" }));
  expectTone(
    await screen.findByRole("button", { name: "Edit Toned Agent" }),
    neutral,
  );
  expectTone(
    screen.getByRole("button", { name: "Preview routing for Toned Agent" }),
    neutral,
  );
  expectTone(
    screen.getByRole("button", { name: "Delete Toned Agent" }),
    danger,
  );
  fireEvent.click(screen.getByRole("button", { name: "Delete Toned Agent" }));
  expect(value.remove).not.toHaveBeenCalled();
  const confirm = screen.getByRole("button", {
    name: "Confirm configuration deletion",
  });
  expectTone(confirm, danger);
  fireEvent.click(confirm);
  expectTone(
    await screen.findByRole("button", { name: "Retry the same deletion" }),
    danger,
  );
  expect(value.remove).toHaveBeenCalledTimes(1);
});

it("marks the generic editable configuration deletion opener Danger", async () => {
  const instruction = resource(
    EntityKind.TEMPLATE,
    { name: "Toned instructions", contents: "Server contents" },
    3n,
  );
  const value = fixture([instruction]);
  render(value.view(<Settings />));
  fireEvent.click(screen.getByRole("button", { name: "Instructions" }));
  const opener = await screen.findByRole("button", {
    name: "Delete Toned instructions",
  });
  expectTone(opener, danger);
  expectTone(
    screen.getByRole("button", { name: "Edit Toned instructions" }),
    neutral,
  );
});

it("marks only the custom Provider delete opener Danger beside Edit and Copy", async () => {
  const custom = resource(EntityKind.PROVIDER, {
    name: "Custom fixture",
    enabled: true,
  });
  const entries = [
    create(ProviderInventoryEntrySchema, {
      presetId: ProviderPresetId.OLLAMA,
      displayName: "Local fixture",
      enabled: true,
      accountCountsAvailable: true,
    }),
    create(ProviderInventoryEntrySchema, {
      providerId: custom.id,
      provider: custom,
      displayName: "Custom fixture",
      enabled: true,
      accountCountsAvailable: false,
    }),
  ];
  const value = fixture([custom], { providerEntries: entries });
  render(value.view(<Settings visible />));
  fireEvent.click(screen.getByRole("button", { name: "API Providers" }));
  const name = await screen.findByText("Custom fixture", {
    selector: ".api-provider-name span",
  });
  const row = within(name.closest("article")!);
  expectTone(
    row.getByRole("button", { name: /^Delete custom provider/ }),
    danger,
  );
  expectTone(
    row.getByRole("button", { name: /^Edit custom provider/ }),
    neutral,
  );
  const preset = within(
    screen
      .getByText("Local fixture", { selector: ".api-provider-name span" })
      .closest("article")!,
  );
  for (const button of preset.getAllByRole("button"))
    expect(button.getAttribute("data-action-tone")).not.toBe("danger");
});
