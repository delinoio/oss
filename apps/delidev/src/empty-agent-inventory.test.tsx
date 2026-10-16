// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  createQueryOptions,
  TransportProvider,
} from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { StrictMode } from "react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceQuery,
  ResourceSchema,
  ResourceService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";
import { copy, i18n } from "./localization";
import { scrollChoiceValue } from "./test-scroll-picker";

function worker() {
  return create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.AGENT,
    schemaVersion: 4,
    revision: 1n,
    documentJson: encode({
      name: "Worker",
      harness: "codex",
      routes: [
        {
          model: {
            subscription_service: "chatgpt",
            native_id: "fixture-model",
          },
          accounts: [{ id: newRequestId(), weight: 1 }],
        },
      ],
      templates: [],
      options: { permission: "default" },
    }),
  });
}
function fixture(
  read: (
    token: string,
  ) =>
    | Promise<{ resources: Resource[]; nextPageToken?: string }>
    | { resources: Resource[]; nextPageToken?: string },
) {
  const reads = vi.fn(read),
    selected = worker(),
    invoke = vi.fn(),
    newInvoke = vi.fn(),
    change = vi.fn();
  const transport = createRouterTransport((router) =>
    router.service(ResourceService, {
      listResources: (request) => reads(request.filter?.pageToken ?? ""),
      getResource: (request) => ({ resource: { ...selected, id: request.id } }),
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View({
    active = true,
    allowed,
    value = "",
    locked = false,
    source = transport,
    pending = false,
    withNew = false,
  }: {
    active?: boolean;
    allowed?: string[];
    value?: string;
    locked?: boolean;
    source?: typeof transport;
    pending?: boolean;
    withNew?: boolean;
  }) {
    return (
      <TransportProvider transport={source}>
        <QueryClientProvider client={client}>
          <fieldset disabled={locked}>
            <ResourceChoice
              label="Agent Worker"
              kind={EntityKind.AGENT}
              value={value}
              active={active}
              allowed={allowed}
              showStatus
              change={change}
              emptyInventoryActionPending={pending}
              createAction={
                withNew
                  ? {
                      label: "New",
                      name: "New agent worker",
                      invoke: newInvoke,
                    }
                  : undefined
              }
              emptyInventoryAction={{
                label: copy("new-session.createAgentWorker"),
                invoke,
              }}
            />
          </fieldset>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return {
    View,
    client,
    transport,
    reads,
    invoke,
    newInvoke,
    change,
    selected,
  };
}

it.each(["en", "ko"])(
  "shows a localized native creation button only after a current empty read (%s)",
  async (language) => {
    await i18n.changeLanguage(language);
    const f = fixture(() => ({ resources: [] }));
    const view = render(<f.View />);
    const label =
      language === "en" ? "Create agent worker" : "에이전트 워커 생성";
    const action = await screen.findByRole("button", { name: label });
    expect(action).toHaveProperty("type", "button");
    expect(action.querySelector("svg")?.getAttribute("aria-hidden")).toBe(
      "true",
    );
    expect(screen.queryByRole("combobox", { name: "Agent Worker" })).toBeNull();
    expect(
      screen.queryByText(
        "No selectable Agent Worker choices are on this page.",
      ),
    ).toBeNull();
    action.focus();
    expect(document.activeElement).toBe(action);
    fireEvent.click(action);
    expect(f.invoke).toHaveBeenCalledTimes(1);
    expect(f.change).not.toHaveBeenCalled();
    expect(f.reads).toHaveBeenCalledTimes(1);
    view.rerender(<f.View locked />);
    expect(action.matches(":disabled")).toBe(true);
    await i18n.changeLanguage("en");
  },
);

it.each([
  { allowed: undefined },
  { allowed: [] },
  { allowed: [newRequestId()] },
])(
  "keeps nonempty inventory distinct from project-filtered emptiness (%j)",
  async ({ allowed }) => {
    const row = worker(),
      f = fixture(() => ({ resources: [row] }));
    render(<f.View allowed={allowed} />);
    const choice = screen.getByRole("combobox", { name: "Agent Worker" });
    if (allowed === undefined) {
      fireEvent.click(choice);
      fireEvent.click(await screen.findByRole("option", { name: "Worker" }));
      await waitFor(() => expect(f.change).toHaveBeenCalled());
      expect(f.change.mock.calls[0][0]).toBe(row.id);
    } else {
      expect(
        await screen.findByText(/match the selected project's restrictions/),
      ).toBeTruthy();
    }
    expect(
      screen.queryByRole("button", { name: "Create agent worker" }),
    ).toBeNull();
    expect(
      screen.queryByText(
        "No selectable Agent Worker choices are on this page.",
      ),
    ).toBeNull();
  },
);

it.each([Code.PermissionDenied, Code.Unavailable, Code.DataLoss])(
  "retains current read failures without inventing absence (%s)",
  async (code) => {
    const f = fixture(() => {
      throw new ConnectError("Original failure", code);
    });
    render(<f.View />);
    expect(
      await screen.findByRole("button", { name: "Retry current read" }),
    ).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Create agent worker" }),
    ).toBeNull();
  },
);

it("rejects a malformed raw row before a deny-all restriction can hide it", async () => {
  const row = worker();
  row.kind = EntityKind.PROJECT;
  const f = fixture(() => ({ resources: [row] }));
  render(<f.View allowed={[]} />);
  expect(
    await screen.findByRole("button", { name: "Retry current read" }),
  ).toBeTruthy();
  expect(screen.queryByText(/match the selected project/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("keeps empty continuation and later final pages as ordinary pagination", async () => {
  const f = fixture((token) => ({
    resources: [],
    nextPageToken: token ? "" : "next",
  }));
  render(<f.View />);
  expect(
    await screen.findByText(/More Agent Worker choices are available/),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  const continuation = await screen.findByRole("button", { name: /Load more/ });
  fireEvent.click(continuation);
  await waitFor(() => expect(f.reads).toHaveBeenCalledWith("next"));
  await waitFor(() =>
    expect(
      screen.queryByText(/More Agent Worker choices are available/),
    ).toBeNull(),
  );
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

async function loadTerminalPage(f: ReturnType<typeof fixture>) {
  await screen.findByText(/More Agent Worker choices are available/);
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: /Load more/ }));
  await waitFor(() => expect(f.reads).toHaveBeenCalledWith("next"));
  await waitFor(() =>
    expect(
      screen.queryByText(/More Agent Worker choices are available/),
    ).toBeNull(),
  );
}

it("shows project restriction guidance when a terminal page is fully excluded after an empty first page", async () => {
  const later = worker(),
    f = fixture((token) =>
      token ? { resources: [later] } : { resources: [], nextPageToken: "next" },
    );
  render(<f.View allowed={[]} />);
  await loadTerminalPage(f);
  expect(
    await screen.findByText(/match the selected project's restrictions/),
  ).toBeTruthy();
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  expect(f.change).not.toHaveBeenCalled();
});

it("keeps the continuation state ahead of restriction guidance until the terminal page loads", async () => {
  const f = fixture((token) =>
    token
      ? { resources: [worker()] }
      : { resources: [], nextPageToken: "next" },
  );
  render(<f.View allowed={[]} />);
  expect(
    await screen.findByText(/More Agent Worker choices are available/),
  ).toBeTruthy();
  expect(screen.queryByText(/match the selected project/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("does not report restriction when every reached page is raw-empty", async () => {
  const f = fixture((token) => ({
    resources: [],
    nextPageToken: token ? "" : "next",
  }));
  render(<f.View allowed={[]} />);
  await loadTerminalPage(f);
  expect(screen.queryByText(/match the selected project/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

async function loadAllPages(f: ReturnType<typeof fixture>, pages: number) {
  await screen.findByText(/More Agent Worker choices are available/);
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  for (let index = 1; index < pages; index++) {
    fireEvent.click(await screen.findByRole("button", { name: /Load more/ }));
    await waitFor(() => expect(f.reads).toHaveBeenCalledWith(`p${index + 1}`));
  }
  await waitFor(() =>
    expect(
      screen.queryByText(/More Agent Worker choices are available/),
    ).toBeNull(),
  );
}
const chained = (excluded: Resource, pages: number) => (token: string) => {
  const index = token ? Number(token.slice(1)) : 1;
  return {
    resources: index === 1 ? [excluded] : [],
    nextPageToken: index < pages ? `p${index + 1}` : "",
  };
};

it("keeps restriction guidance when the excluded worker page was evicted from a four-page chain", async () => {
  const f = fixture(chained(worker(), 4));
  render(<f.View allowed={[]} />);
  await loadAllPages(f, 4);
  expect(
    await screen.findByText(/match the selected project's restrictions/),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  expect(f.change).not.toHaveBeenCalled();
});

it("keeps restriction guidance for the same excluded worker in a three-page chain", async () => {
  const f = fixture(chained(worker(), 3));
  render(<f.View allowed={[]} />);
  await loadAllPages(f, 3);
  expect(
    await screen.findByText(/match the selected project's restrictions/),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("drops stale restriction evidence when the chain is replaced by an empty first page", async () => {
  let empty = false;
  const excluded = worker(),
    f = fixture((token) =>
      empty
        ? { resources: [] }
        : token
          ? { resources: [] }
          : { resources: [excluded], nextPageToken: "next" },
    );
  const view = render(<f.View allowed={[]} />);
  await loadTerminalPage(f);
  expect(
    await screen.findByText(/match the selected project's restrictions/),
  ).toBeTruthy();
  empty = true;
  // A scope change (project restriction key) replaces the validated chain.
  view.rerender(<f.View allowed={["other-project-worker"]} />);
  expect(
    await screen.findByRole("button", { name: "Create agent worker" }),
  ).toBeTruthy();
  expect(screen.queryByText(/match the selected project/)).toBeNull();
});

it("keeps the continuation status and no creation entry for a continuing empty first page followed by restricted workers", async () => {
  const f = fixture((token) =>
    token === "p3"
      ? { resources: [worker()] }
      : token
        ? { resources: [worker()], nextPageToken: "p3" }
        : { resources: [], nextPageToken: "p2" },
  );
  render(<f.View allowed={[]} />);
  expect(
    await screen.findByText(/More Agent Worker choices are available/),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("combobox", { name: "Agent Worker" }));
  fireEvent.click(await screen.findByRole("button", { name: /Load more/ }));
  await waitFor(() => expect(f.reads).toHaveBeenCalledWith("p2"));
  expect(
    await screen.findByText(/More Agent Worker choices are available/),
  ).toBeTruthy();
  expect(screen.queryByText(/match the selected project/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("lists an allowed worker from a later page without restriction guidance", async () => {
  const later = worker(),
    f = fixture((token) =>
      token ? { resources: [later] } : { resources: [], nextPageToken: "next" },
    );
  render(<f.View allowed={[later.id]} />);
  await loadTerminalPage(f);
  fireEvent.click(await screen.findByRole("option", { name: "Worker" }));
  await waitFor(() => expect(f.change).toHaveBeenCalled());
  expect(f.change.mock.calls[0][0]).toBe(later.id);
  expect(screen.queryByText(/match the selected project/)).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("retains an off-page selected identity when the raw inventory is empty", async () => {
  const f = fixture(() => ({ resources: [] }));
  render(<f.View value={f.selected.id} />);
  await screen.findByText(
    "The selected Agent Worker is outside this page. Its exact identity remains selected.",
  );
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe(f.selected.id);
  expect(f.change).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("removes absence during refresh and failure, then restores selection after inventory invalidation", async () => {
  let release!: (result: { resources: Resource[] }) => void;
  const pending = new Promise<{ resources: Resource[] }>((resolve) => {
    release = resolve;
  });
  const f = fixture(() => ({ resources: [] }));
  render(<f.View />);
  await screen.findByRole("button", { name: "Create agent worker" });
  f.reads.mockImplementationOnce(() => pending);
  const options = createQueryOptions(
    ResourceQuery.listResources,
    {
      filter: { kind: EntityKind.AGENT, pageSize: 50, pageToken: "" },
    },
    { transport: f.transport },
  );
  await act(async () => {
    await f.client.invalidateQueries({ queryKey: options.queryKey });
  });
  expect(await screen.findByText("Loading Agent Worker choices…")).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  const row = worker();
  await act(async () => release({ resources: [row] }));
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(
    scrollChoiceValue(screen.getByRole("combobox", { name: "Agent Worker" })),
  ).toBe("");
  expect(f.change).not.toHaveBeenCalled();
  f.reads.mockImplementationOnce(() => {
    throw new ConnectError("Refresh denied", Code.PermissionDenied);
  });
  await act(async () => {
    await f.client.invalidateQueries({ queryKey: options.queryKey });
  });
  await screen.findByText(/Showing cached Agent Worker choices/);
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("revalidates empty evidence after navigation instead of trusting retained rows", async () => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = fixture(() => ({ resources: [] }));
  const view = render(<f.View />);
  await screen.findByRole("button", { name: "Create agent worker" });
  view.rerender(<f.View active={false} />);
  f.reads.mockImplementationOnce(async () => {
    await pending;
    return { resources: [] };
  });
  view.rerender(<f.View />);
  expect(await screen.findByText("Loading Agent Worker choices…")).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  await act(async () => release());
  expect(
    await screen.findByRole("button", { name: "Create agent worker" }),
  ).toBeTruthy();
});

it("does not reuse an empty success after its current refresh fails", async () => {
  const f = fixture(() => ({ resources: [] }));
  render(<f.View />);
  await screen.findByRole("button", { name: "Create agent worker" });
  f.reads.mockImplementationOnce(() => {
    throw new ConnectError("Empty refresh denied", Code.PermissionDenied);
  });
  const options = createQueryOptions(
    ResourceQuery.listResources,
    {
      filter: { kind: EntityKind.AGENT, pageSize: 50, pageToken: "" },
    },
    { transport: f.transport },
  );
  await act(async () => {
    await f.client.invalidateQueries({ queryKey: options.queryKey });
  });
  await screen.findByText(/Showing cached Agent Worker choices/);
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
});

it("requires a new complete read when the authenticated transport changes", async () => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = fixture(() => ({ resources: [] }));
  const view = render(<f.View />);
  await screen.findByRole("button", { name: "Create agent worker" });
  const source = createRouterTransport((router) =>
    router.service(ResourceService, {
      listResources: async () => {
        await gate;
        return { resources: [worker()] };
      },
    }),
  );
  view.rerender(<f.View source={source} />);
  expect(await screen.findByText("Loading Agent Worker choices…")).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  await act(async () => release());
  await waitFor(() =>
    expect(screen.queryByText("Loading Agent Worker choices…")).toBeNull(),
  );
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(f.change).not.toHaveBeenCalled();
});

it("withholds the creation entry while the caller still restores a retained Agent", async () => {
  const f = fixture(() => ({ resources: [] }));
  const view = render(<f.View pending />);
  expect(await screen.findByText("Loading Agent Worker choices…")).toBeTruthy();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  expect(
    screen.queryByRole("button", { name: "Create agent worker" }),
  ).toBeNull();
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  view.rerender(<f.View />);
  expect(
    await screen.findByRole("button", { name: "Create agent worker" }),
  ).toBeTruthy();
  expect(screen.queryByText("Loading Agent Worker choices…")).toBeNull();
  expect(f.change).not.toHaveBeenCalled();
});

it("withholds the dropdown New action while a proven-empty inventory awaits restoration", async () => {
  const f = fixture(() => ({ resources: [] }));
  const view = render(<f.View pending withNew />);
  expect(await screen.findByText("Loading Agent Worker choices…")).toBeTruthy();
  const picker = screen.getByRole("combobox", { name: "Agent Worker" });
  await waitFor(() => expect(picker).toHaveProperty("disabled", false));
  fireEvent.click(picker);
  expect(screen.queryByRole("option", { name: "New agent worker" })).toBeNull();
  expect(f.newInvoke).not.toHaveBeenCalled();
  // Once restoration settles the empty-inventory shortcut replaces the picker.
  view.rerender(<f.View withNew />);
  expect(
    await screen.findByRole("button", { name: "Create agent worker" }),
  ).toBeTruthy();
  expect(screen.queryByRole("combobox", { name: "Agent Worker" })).toBeNull();
  expect(f.newInvoke).not.toHaveBeenCalled();
});

it("keeps the dropdown New action for a populated or project-filtered inventory", async () => {
  const f = fixture(() => ({ resources: [worker()] }));
  render(<f.View allowed={[newRequestId()]} withNew />);
  const picker = screen.getByRole("combobox", { name: "Agent Worker" });
  await screen.findByText(/match the selected project's restrictions/);
  await waitFor(() => expect(picker).toHaveProperty("disabled", false));
  fireEvent.click(picker);
  fireEvent.click(
    await screen.findByRole("option", { name: "New agent worker" }),
  );
  expect(f.newInvoke).toHaveBeenCalledTimes(1);
  expect(f.change).not.toHaveBeenCalled();
});

function deferredEmpty() {
  let release!: (result: { resources: Resource[] }) => void;
  const pending = new Promise<{ resources: Resource[] }>((resolve) => {
    release = resolve;
  });
  return { pending, release };
}
function listOptions(f: ReturnType<typeof fixture>) {
  return createQueryOptions(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.AGENT, pageSize: 50, pageToken: "" } },
    { transport: f.transport },
  );
}
function Composer({
  f,
  strict = false,
}: {
  f: ReturnType<typeof fixture>;
  strict?: boolean;
}) {
  const body = (
    <>
      <f.View />
      <input aria-label="First message" />
    </>
  );
  return strict ? <StrictMode>{body}</StrictMode> : body;
}

it.each([false, true])(
  "moves focus from the loading selector to the creation button and back on refresh (strict %s)",
  async (strict) => {
    const first = deferredEmpty();
    const f = fixture(() => ({ resources: [] }));
    f.reads.mockImplementationOnce(() => first.pending);
    render(<Composer f={f} strict={strict} />);
    const picker = await screen.findByRole("combobox", {
      name: "Agent Worker",
    });
    picker.focus();
    expect(document.activeElement).toBe(picker);
    await act(async () => first.release({ resources: [] }));
    const action = await screen.findByRole("button", {
      name: "Create agent worker",
    });
    expect(document.activeElement).toBe(action);
    // Refresh replaces the focused creation button with the selector.
    const second = deferredEmpty();
    f.reads.mockImplementationOnce(() => second.pending);
    await act(async () => {
      await f.client.invalidateQueries({ queryKey: listOptions(f).queryKey });
    });
    const replacement = await screen.findByRole("combobox", {
      name: "Agent Worker",
    });
    expect(document.activeElement).toBe(replacement);
    await act(async () => second.release({ resources: [] }));
    expect(
      document.activeElement ===
        (await screen.findByRole("button", { name: "Create agent worker" })),
    ).toBe(true);
    expect(f.invoke).not.toHaveBeenCalled();
    expect(f.change).not.toHaveBeenCalled();
  },
);

it("preserves focus that already moved to another composer control", async () => {
  const first = deferredEmpty();
  const f = fixture(() => ({ resources: [] }));
  f.reads.mockImplementationOnce(() => first.pending);
  render(<Composer f={f} />);
  const picker = await screen.findByRole("combobox", { name: "Agent Worker" });
  picker.focus();
  const message = screen.getByLabelText("First message");
  message.focus();
  await act(async () => first.release({ resources: [] }));
  await screen.findByRole("button", { name: "Create agent worker" });
  expect(document.activeElement).toBe(message);
  // The unfocused creation button never pulls focus back on refresh either.
  const second = deferredEmpty();
  f.reads.mockImplementationOnce(() => second.pending);
  await act(async () => {
    await f.client.invalidateQueries({ queryKey: listOptions(f).queryKey });
  });
  await screen.findByRole("combobox", { name: "Agent Worker" });
  expect(document.activeElement).toBe(message);
});

it("does not take focus when nothing in the swapped control owned it", async () => {
  const first = deferredEmpty();
  const f = fixture(() => ({ resources: [] }));
  f.reads.mockImplementationOnce(() => first.pending);
  render(<Composer f={f} />);
  await screen.findByRole("combobox", { name: "Agent Worker" });
  expect(document.activeElement).toBe(document.body);
  await act(async () => first.release({ resources: [] }));
  await screen.findByRole("button", { name: "Create agent worker" });
  expect(document.activeElement).toBe(document.body);
});

it("dismisses an open popup when focus moves to the creation button", async () => {
  const first = deferredEmpty();
  const f = fixture(() => ({ resources: [] }));
  f.reads.mockImplementationOnce(() => first.pending);
  render(<Composer f={f} />);
  const picker = await screen.findByRole("combobox", { name: "Agent Worker" });
  picker.focus();
  fireEvent.click(picker);
  expect(screen.getByRole("listbox")).toBeTruthy();
  await act(async () => first.release({ resources: [] }));
  const action = await screen.findByRole("button", {
    name: "Create agent worker",
  });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(document.activeElement).toBe(action);
});

it("does not focus a disabled or inactive replacement selector", async () => {
  const f = fixture(() => ({ resources: [] }));
  const view = render(<Composer f={f} />);
  const action = await screen.findByRole("button", {
    name: "Create agent worker",
  });
  action.focus();
  expect(document.activeElement).toBe(action);
  // Deactivating the surface swaps to a disabled selector without a handoff.
  view.rerender(
    <>
      <f.View active={false} />
      <input aria-label="First message" />
    </>,
  );
  const picker = await screen.findByRole("combobox", { name: "Agent Worker" });
  expect(picker.matches(":disabled")).toBe(true);
  expect(document.activeElement).not.toBe(picker);
});
