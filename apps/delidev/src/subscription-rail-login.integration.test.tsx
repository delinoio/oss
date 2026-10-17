// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SubscriptionService,
  SubscriptionLoginState as State,
  SubscriptionServiceId,
  SystemCapability,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { OAuthNativeProvider } from "./account-oauth";
import { SettingsLifetime } from "./settings-lifetime";
import { document as accountDocument, encode } from "./documents";
import { SubscriptionRail } from "./subscription-rail";
import { useSubscriptionLogin } from "./subscription-login";

const authorizationUrl =
  "https://auth.openai.com/oauth/authorize?state=fixture-original-state-123456&redirect_uri=http%3A%2F%2Flocalhost%3A1457%2Fauth%2Fcallback&response_type=code&code_challenge_method=S256";

function connectedAccount(alias: string): Resource {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    schemaVersion: 2,
    revision: 1n,
    documentJson: encode({
      alias,
      type: "subscription",
      subscription_service: "chatgpt",
      connection: { id: newRequestId() },
      quota: [],
    }),
  });
}

/** One server state shared by the Settings login flow and the rail, behind a
 * single transport and QueryClient (the production sharing model). */
function fixture(initial: Resource[]) {
  let accounts = [...initial];
  let created: Resource | undefined;
  let state = State.WAITING;
  let operation = "";
  const generation = newRequestId();
  const reads: string[] = [];
  const gate = { hold: undefined as Promise<void> | undefined };
  const replace = (next: Resource) => {
    accounts = accounts.map((row) => (row.id === next.id ? next : row));
    return next;
  };
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        stopping: false,
        capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: async (request) => {
        reads.push(request.filter?.pageToken ?? "");
        // A held read keeps the snapshot it started with.
        const snapshot = [...accounts];
        if (gate.hold) await gate.hold;
        return { resources: snapshot, nextPageToken: "" };
      },
      getResource: (request) => ({
        resource: accounts.find((row) => row.id === request.id),
      }),
    });
    router.service(ConfigurationService, {
      saveConfiguration: (request) => {
        const existing = accounts.find(
          (row) => row.id === request.mutation?.id,
        );
        if (existing)
          return {
            requestId: request.mutation!.requestId,
            resource: replace(
              create(ResourceSchema, {
                ...existing,
                revision: existing.revision + 1n,
                documentJson: request.documentJson,
              }),
            ),
          };
        created = create(ResourceSchema, {
          id: newRequestId(),
          kind: EntityKind.ACCOUNT,
          schemaVersion: 2,
          revision: 1n,
          documentJson: request.documentJson,
        });
        accounts = [...accounts, created];
        return { requestId: request.mutation!.requestId, resource: created };
      },
    });
    router.service(SubscriptionService, {
      requestSubscription: (request) => {
        operation = request.mutation!.requestId;
        const account = replace(
          create(ResourceSchema, {
            ...created!,
            revision: created!.revision + 1n,
            documentJson: encode({
              ...accountDocument(created!),
              subscription: {
                server_operation: { id: operation },
                pending: { id: operation },
              },
            }),
          }),
        );
        created = account;
        return { account, operationId: operation };
      },
      getSubscriptionProgress: () => ({
        state,
        url: state === State.WAITING ? authorizationUrl : "",
        suggestedName: "Work",
        generation,
      }),
      cancelSubscription: () => ({ account: created }),
      forwardSubscriptionCallback: () => ({ accepted: true }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function Body() {
    const flow = useSubscriptionLogin(true, () => undefined);
    return flow.body ? (
      flow.body
    ) : (
      <button onClick={() => flow.begin(SubscriptionServiceId.ChatGPT)}>
        Add account
      </button>
    );
  }
  const view = (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <OAuthNativeProvider
            control={async () => ({ generation: newRequestId() })}
          >
            <SettingsLifetime>{() => <Body />}</SettingsLifetime>
          </OAuthNativeProvider>
          <SubscriptionRail enabled manage={() => undefined} />
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  return {
    view,
    reads,
    gate,
    created: () => created,
    fail: () => {
      state = State.FAILED;
    },
    succeed: () => {
      state = State.SUCCEEDED;
      replace(
        create(ResourceSchema, {
          ...created!,
          revision: created!.revision + 1n,
          documentJson: encode({
            ...accountDocument(created!),
            connection: { id: newRequestId() },
            subscription: {
              generation,
              server_operation: { id: operation },
            },
          }),
        }),
      );
    },
    railButtons: () => screen.queryAllByRole("button", { name: /^ChatGPT · / }),
  };
}

async function beginLogin() {
  fireEvent.click(screen.getByRole("button", { name: "Add account" }));
  await screen.findByRole(
    "button",
    { name: "Open browser again" },
    { timeout: 3000 },
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

it("shows a newly connected account and its saved alias in the rail without a timer or focus change", async () => {
  const f = fixture([connectedAccount("Personal")]);
  render(f.view);
  await screen.findByRole("button", { name: /^ChatGPT · Personal/ });
  await beginLogin();
  // The created account is not connected yet: no rail entry for it.
  expect(f.railButtons()).toHaveLength(1);
  f.succeed();
  const input = await screen.findByLabelText(
    "Account name",
    {},
    { timeout: 3000 },
  );
  await waitFor(() => expect(f.railButtons()).toHaveLength(2));
  expect(
    screen.getByRole("button", { name: /^ChatGPT · ChatGPT/ }),
  ).toBeTruthy();
  fireEvent.change(input, { target: { value: "Work" } });
  fireEvent.click(screen.getByRole("button", { name: "Save account name" }));
  await screen.findByRole("button", { name: /^ChatGPT · Work/ });
  // Same service, independent identities, no duplicates after repeated notices.
  expect(
    f.railButtons().map((button) => button.getAttribute("aria-label")),
  ).toEqual([
    "ChatGPT · Personal · Quota unavailable",
    "ChatGPT · Work · Quota unavailable",
  ]);
  // Only the accepted first page was reread; the 30 second cadence never ran.
  expect(f.reads.every((token) => token === "")).toBe(true);
});

it("shows the first connected account in an initially empty rail", async () => {
  const f = fixture([]);
  render(f.view);
  await beginLogin();
  expect(f.railButtons()).toHaveLength(0);
  f.succeed();
  await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  await waitFor(() => expect(f.railButtons()).toHaveLength(1));
});

it("creates no connected rail entry when the initial login fails", async () => {
  const f = fixture([connectedAccount("Personal")]);
  render(f.view);
  await screen.findByRole("button", { name: /^ChatGPT · Personal/ });
  await beginLogin();
  f.fail();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
  });
  expect(f.created()).toBeTruthy();
  expect(
    f.railButtons().map((button) => button.getAttribute("aria-label")),
  ).toEqual(["ChatGPT · Personal · Quota unavailable"]);
});

it("queues the notification behind an in-flight read and publishes the current inventory", async () => {
  const f = fixture([connectedAccount("Personal")]);
  render(f.view);
  await screen.findByRole("button", { name: /^ChatGPT · Personal/ });
  let release!: () => void;
  f.gate.hold = new Promise<void>((resolve) => {
    release = resolve;
  });
  await beginLogin();
  // The creation notice started a held read of the old snapshot.
  await waitFor(() => expect(f.reads.length).toBeGreaterThanOrEqual(2));
  f.gate.hold = undefined;
  f.succeed();
  await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  expect(f.railButtons()).toHaveLength(1);
  release();
  await waitFor(() => expect(f.railButtons()).toHaveLength(2));
  expect(f.reads.every((token) => token === "")).toBe(true);
});

it("rereads current saved resources even while the document is hidden", async () => {
  const f = fixture([connectedAccount("Personal")]);
  render(f.view);
  await screen.findByRole("button", { name: /^ChatGPT · Personal/ });
  await beginLogin();
  const visibility = vi
    .spyOn(window.document, "visibilityState", "get")
    .mockReturnValue("hidden");
  fireEvent(window.document, new Event("visibilitychange"));
  f.succeed();
  await screen.findByLabelText("Account name", {}, { timeout: 3000 });
  await waitFor(() => expect(f.railButtons()).toHaveLength(2));
  visibility.mockRestore();
});
