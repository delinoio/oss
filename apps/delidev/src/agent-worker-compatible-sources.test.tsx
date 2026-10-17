// SPDX-License-Identifier: Apache-2.0
// Agent Worker API source choices are the server-filtered saved providers that
// own a selectable account compatible with the harness protocol.
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ApiProtocol,
  ConfigurationService,
  EntityKind,
  ProviderInventoryCapability,
  ProviderService,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type GetResourceRequest,
  type ListProviderInventoryRequest,
  type Resource,
} from "@delinoio/delidev-api-client";
import { AgentWorkerWizard } from "./agent-worker-wizard";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { chooseScrollOption } from "./test-scroll-picker";

afterEach(cleanup);

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

interface Options {
  /** Whether the server advertises the compatible-account filter capability. */
  filtered?: boolean;
  /** Provider ids the server returns for the requested protocol. */
  eligible?: (protocol: ApiProtocol) => string[];
  /** The page token the server hands out after the first page. */
  nextPageToken?: string;
  initial?: Resource;
  /** Handles inventory reads after the first so a refresh can stall or fail. */
  later?: (request: ListProviderInventoryRequest) => Promise<never> | void;
  /** Scripts each page of a chain: the provider indexes, the continuation and
   * whether that single response advertises the compatible-account filter. */
  pages?: (request: ListProviderInventoryRequest) => {
    indexes: number[];
    next: string;
    advertised: boolean;
  };
}

function fixture(options: Options = {}) {
  const { filtered = true, nextPageToken = "" } = options;
  const providers = ["Responses API", "Messages API", "Chat API"].map(
    (name, index) =>
      row(EntityKind.PROVIDER, {
        name,
        protocol: ["openai-responses", "anthropic-messages", "openai-chat"][
          index
        ],
        endpoint: "https://fixture.test/v1",
        enabled: true,
        authentication: "keyless",
      }),
  );
  const [responses, messages, chat] = providers;
  const eligible =
    options.eligible ??
    ((protocol: ApiProtocol) =>
      protocol === ApiProtocol.OPENAI_RESPONSES
        ? [responses.id]
        : protocol === ApiProtocol.ANTHROPIC_MESSAGES
          ? [messages.id]
          : protocol === ApiProtocol.OPENAI_CHAT
            ? [chat.id]
            : providers.map((provider) => provider.id));
  const names = new Map(
    providers.map((provider, index) => [
      provider.id,
      ["Responses API", "Messages API", "Chat API"][index],
    ]),
  );
  const requests: ListProviderInventoryRequest[] = [];
  const inventory = vi.fn((request: ListProviderInventoryRequest) => {
    requests.push(request);
    if (requests.length > 1) {
      const pending = options.later?.(request);
      if (pending) return pending;
    }
    const scripted = options.pages?.(request);
    if (scripted)
      return {
        entries: scripted.indexes.map((index) => ({
          providerId: providers[index].id,
          provider: providers[index],
          displayName: names.get(providers[index].id)!,
          enabled: true,
        })),
        nextPageToken: scripted.next,
        capabilities: [
          ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
          ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
          ...(scripted.advertised
            ? [
                ProviderInventoryCapability.COMPATIBLE_ACCOUNT_API_PROTOCOL_FILTER_V1,
              ]
            : []),
        ],
      };
    const ids = eligible(request.compatibleAccountApiProtocol);
    return {
      entries: (filtered || request.pageToken === ""
        ? providers.filter(
            (provider) =>
              !filtered ||
              request.compatibleAccountApiProtocol ===
                ApiProtocol.UNSPECIFIED ||
              ids.includes(provider.id),
          )
        : []
      ).map((provider) => ({
        providerId: provider.id,
        provider,
        displayName: names.get(provider.id)!,
        enabled: true,
      })),
      nextPageToken: request.pageToken ? "" : nextPageToken,
      capabilities: [
        ProviderInventoryCapability.ACCOUNT_PROVIDER_FILTER,
        ProviderInventoryCapability.ACCOUNT_TYPE_FILTER,
        ...(filtered
          ? [
              ProviderInventoryCapability.COMPATIBLE_ACCOUNT_API_PROTOCOL_FILTER_V1,
            ]
          : []),
      ],
    };
  });
  const records = [...providers];
  if (options.initial) records.push(options.initial);
  const get = vi.fn((request: GetResourceRequest) => ({
    resource: records.find((record) => record.id === request.id),
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        protocolVersion: 2,
        capabilities: [SystemCapability.INLINE_WORKER_MODELS_V1],
      }),
    });
    router.service(ProviderService, { listProviderInventory: inventory });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
      getResource: get,
    });
    router.service(ConfigurationService, { saveAgentWorker: vi.fn() });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const saved = vi.fn(),
    cancel = vi.fn();
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <AgentWorkerWizard
            active
            initial={options.initial}
            saved={saved}
            cancel={cancel}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { providers, requests, inventory, get, saved, client };
}

async function openSource(harness: string) {
  fireEvent.click(await screen.findByRole("radio", { name: harness }));
  const picker = screen.getByRole("combobox", { name: "Account source 1" });
  await waitFor(() =>
    expect((picker as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(picker);
  return picker;
}
const optionIds = (picker: HTMLElement) =>
  [
    ...(globalThis.document
      .getElementById(picker.getAttribute("aria-controls") ?? "")
      ?.querySelectorAll<HTMLElement>("[data-picker-id]") ?? []),
  ].map((node) => node.dataset.pickerId);

it.each([
  ["Codex", ApiProtocol.OPENAI_RESPONSES],
  ["Claude Code", ApiProtocol.ANTHROPIC_MESSAGES],
  ["OpenCode", ApiProtocol.OPENAI_CHAT],
  ["Grok Build", ApiProtocol.OPENAI_CHAT],
])(
  "requests the %s protocol filter and offers only the returned providers",
  async (harness, protocol) => {
    const f = fixture();
    const picker = await openSource(harness);
    await waitFor(() =>
      expect(optionIds(picker).some((id) => id?.startsWith("api:"))).toBe(true),
    );
    expect(f.requests.length).toBeGreaterThan(0);
    expect(
      f.requests.every(
        (request) =>
          request.compatibleAccountApiProtocol === protocol &&
          request.enabledOnly === true,
      ),
    ).toBe(true);
    const apiIds = optionIds(picker).filter((id) => id?.startsWith("api:"));
    expect(apiIds).toHaveLength(1);
    expect(screen.queryByText(/Update the server/)).toBeNull();
    expect(screen.queryByText(/No enabled API provider/)).toBeNull();
  },
);

it("shows localized empty guidance, not a stale list, when no provider qualifies", async () => {
  fixture({ eligible: () => [] });
  const picker = await openSource("Codex");
  await screen.findByText(/No enabled API provider has a connected account/);
  expect(optionIds(picker).some((id) => id?.startsWith("api:"))).toBe(false);
  expect(optionIds(picker).some((id) => id?.startsWith("subscription:"))).toBe(
    true,
  );
});

it("withholds the unfiltered list and shows update guidance on an older server", async () => {
  const f = fixture({ filtered: false, nextPageToken: "more" });
  const picker = await openSource("Codex");
  await screen.findByText(/Update the server to list API providers/);
  expect(optionIds(picker).some((id) => id?.startsWith("api:"))).toBe(false);
  // Subscription choices stay available and no false empty message appears.
  expect(optionIds(picker).some((id) => id?.startsWith("subscription:"))).toBe(
    true,
  );
  expect(screen.queryByText(/No enabled API provider/)).toBeNull();
  // The hidden unfiltered chain is never paged.
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(f.requests.every((request) => request.pageToken === "")).toBe(true);
});

/** A two-page provider chain whose pages advertise the filter per `advertised`. */
function chain(advertised: { current: boolean[] }) {
  return fixture({
    pages: (request) => {
      const index = request.pageToken ? 1 : 0;
      return {
        indexes: [index],
        next: index ? "" : "page-1",
        advertised: advertised.current[index],
      };
    },
  });
}
const loadMore = async () =>
  fireEvent.click(await screen.findByRole("button", { name: /Load more/ }));
const apiOptions = (picker: HTMLElement) =>
  optionIds(picker).filter((id) => id?.startsWith("api:"));

it("offers every provider when each page of the chain advertises the filter", async () => {
  const f = chain({ current: [true, true] });
  const picker = await openSource("Codex");
  await loadMore();
  await waitFor(() =>
    expect(apiOptions(picker)).toEqual([
      `api:${f.providers[0].id}`,
      `api:${f.providers[1].id}`,
    ]),
  );
  expect(screen.queryByText(/Update the server/)).toBeNull();
});

it("withholds the whole chain when a later page was served without the filter", async () => {
  const f = chain({ current: [true, false] });
  const picker = await openSource("Codex");
  await waitFor(() =>
    expect(apiOptions(picker)).toEqual([`api:${f.providers[0].id}`]),
  );
  await loadMore();
  await screen.findByText(/Update the server to list API providers/);
  expect(apiOptions(picker)).toEqual([]);
  expect(optionIds(picker).some((id) => id?.startsWith("subscription:"))).toBe(
    true,
  );
  expect(screen.queryByText(/No enabled API provider/)).toBeNull();
});

it("withholds the whole chain when a refresh serves an earlier page without the filter", async () => {
  const advertised = { current: [true, true] };
  const f = chain(advertised);
  const picker = await openSource("Codex");
  await loadMore();
  await waitFor(() => expect(apiOptions(picker)).toHaveLength(2));
  // The last response still advertises the filter, but the first does not.
  advertised.current = [false, true];
  void f.client.invalidateQueries();
  await screen.findByText(/Update the server to list API providers/);
  expect(apiOptions(picker)).toEqual([]);
  expect(screen.queryByText(/No enabled API provider/)).toBeNull();
});

it("keeps subscription selection and the draft usable on an older server", async () => {
  fixture({ filtered: false });
  const picker = await openSource("Codex");
  await screen.findByText(/Update the server to list API providers/);
  fireEvent.keyDown(picker, { key: "Escape" });
  await chooseScrollOption(picker, "subscription:chatgpt");
  expect(picker.dataset.value).toBe("subscription:chatgpt");
});

it("keeps a saved API source that no longer qualifies as a retained source", async () => {
  const providerId = newRequestId();
  const accountId = newRequestId();
  const initial = row(
    EntityKind.AGENT,
    {
      name: "Saved worker",
      harness: "codex",
      routes: [
        {
          routing: "priority",
          model: { provider_id: providerId, native_id: "exact-model" },
          accounts: [{ id: accountId, weight: 1 }],
        },
      ],
    },
    4,
  );
  const f = fixture({ initial, eligible: () => [] });
  const picker = await openSource("Codex");
  await screen.findByText(/No enabled API provider has a connected account/);
  expect(picker.dataset.value).toBe(`api:${providerId}`);
  const ids = optionIds(picker);
  expect(ids).toContain(`api:${providerId}`);
  expect(ids.filter((id) => id?.startsWith("api:"))).toEqual([
    `api:${providerId}`,
  ]);
  expect(globalThis.document.body.textContent).toContain("retained source");
  // The draft is never switched or cleared by the filtered read.
  expect(f.saved).not.toHaveBeenCalled();
});

it("hides the empty guidance while a refresh of an empty list is pending", async () => {
  let release: () => void = () => {};
  const f = fixture({
    eligible: () => [],
    later: () =>
      new Promise<never>((_, reject) => {
        release = () => reject(new ConnectError("stalled", Code.Unavailable));
      }),
  });
  await openSource("Codex");
  await screen.findByText(/No enabled API provider has a connected account/);
  void f.client.invalidateQueries();
  await waitFor(() => expect(f.requests.length).toBeGreaterThan(1));
  expect(screen.queryByText(/No enabled API provider/)).toBeNull();
  release();
  await waitFor(() => expect(f.inventory).toHaveBeenCalled());
});

it("hides the empty guidance when a refresh of an empty list fails", async () => {
  const f = fixture({
    eligible: () => [],
    later: () => {
      throw new ConnectError("unavailable", Code.Unavailable);
    },
  });
  await openSource("Codex");
  await screen.findByText(/No enabled API provider has a connected account/);
  void f.client.invalidateQueries();
  await waitFor(() => expect(f.requests.length).toBeGreaterThan(1));
  await waitFor(() =>
    expect(screen.queryByText(/No enabled API provider/)).toBeNull(),
  );
});
