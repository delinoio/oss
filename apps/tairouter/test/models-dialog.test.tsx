// SPDX-License-Identifier: Apache-2.0
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import {
  createRouterTransport,
  Code,
  ConnectError,
  type ConnectRouter,
} from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  ProviderService,
  PoolService,
  RoutingService,
  ErrorDetailSchema,
  OrganizationSchema,
  GetSessionResponseSchema,
  Role,
  ProviderAuthKind,
  ConnectionState,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { ProvidersPage } from "../src/providers";

const organization = create(OrganizationSchema, {
  id: "org-1",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const other = create(OrganizationSchema, {
  id: "org-2",
  name: "Other Studio",
  role: Role.MEMBER,
});
const session = create(GetSessionResponseSchema, {
  userId: "person",
  displayName: "Alex",
});
const connections = [
  {
    id: "c1",
    name: "Work account",
    providerId: "openai",
    authKind: ProviderAuthKind.API_KEY,
    state: ConnectionState.READY,
  },
  {
    id: "c2",
    name: "Home account",
    providerId: "openai",
    authKind: ProviderAuthKind.API_KEY,
    state: ConnectionState.READY,
  },
];
const sample = [
  {
    id: "model-alpha",
    displayName: "Model Alpha",
    contextLimit: 128000n,
    outputLimit: 8192n,
  },
  { id: "model-beta", displayName: "Model Beta" },
  {
    id: "model-gamma",
    displayName: "",
    contextLimit: 64000n,
    outputLimit: 4096n,
  },
  { id: "model-zero", displayName: "Zero", contextLimit: 0n, outputLimit: 0n },
];
type Request = { organizationId: string; connectionId: string };
const scope = (request: Request): Request => ({
  organizationId: request.organizationId,
  connectionId: request.connectionId,
});
function mount(
  listProviderModels: (request: Request) => object | Promise<object>,
  org = organization,
  staleTime = 0,
) {
  const cache = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime },
      mutations: { retry: false },
    },
  });
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(ProviderService, {
      listProviderConnections: () => ({ connections }),
      listProviderCatalog: () => ({
        providers: [{ id: "openai", displayName: "OpenAI" }],
      }),
      listProviderModels: ((request: Request) =>
        listProviderModels(request)) as never,
    });
    r.service(PoolService, { listPools: () => ({ pools: [] }) });
    r.service(RoutingService, { listRoutes: () => ({ routes: [] }) });
  });
  const ui = (value: typeof organization) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <ProvidersPage organization={value} session={session} />
    </ApiProvider>
  );
  const view = render(ui(org));
  return {
    ...view,
    rerenderWith: (v: typeof organization) => view.rerender(ui(v)),
  };
}
async function open(user: ReturnType<typeof userEvent.setup>, index = 0) {
  const buttons = await screen.findAllByRole("button", {
    name: /^View models/,
  });
  const trigger = buttons[index]!;
  await user.click(trigger);
  return { trigger, dialog: await screen.findByRole("dialog") };
}

describe("connection models dialog", () => {
  it("shows the structured header, rows, labeled limits and footer in returned order", async () => {
    const user = userEvent.setup();
    const calls: Request[] = [];
    mount((request) => {
      calls.push(scope(request));
      return { models: sample };
    });
    const { dialog } = await open(user);
    expect(
      within(dialog).getByRole("heading", { name: "Models from Work account" }),
    ).toBeTruthy();
    expect(dialog.getAttribute("class")).toContain("models-dialog");
    expect(within(dialog).getByText("Only yours in Acme Studio.")).toBeTruthy();
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    const search = within(dialog).getByLabelText("Find a model");
    expect(search.getAttribute("placeholder")).toBe("Search model name or ID");
    expect(document.activeElement).toBe(search);
    const rows = await within(dialog).findAllByRole("listitem");
    expect(rows.map((r) => r.querySelector("code")?.textContent)).toEqual([
      "model-alpha",
      "model-beta",
      "model-gamma",
      "model-zero",
    ]);
    const text = (row: HTMLElement) => row.textContent;
    expect(text(rows[0]!)).toContain("Model Alpha");
    expect(text(rows[0]!)).toContain("Context128,000 tokens");
    expect(text(rows[0]!)).toContain("Output8,192 tokens");
    // Absent limits render text, never a dash or zero.
    expect(text(rows[1]!)).toContain("ContextUnknown");
    expect(text(rows[1]!)).toContain("OutputUnknown");
    // An empty display name falls back to the exact model ID.
    expect(rows[2]!.querySelector("strong")?.textContent).toBe("model-gamma");
    // A known zero stays zero.
    expect(text(rows[3]!)).toContain("Context0 tokens");
    expect(text(rows[3]!)).toContain("Output0 tokens");
    expect(text(rows[3]!)).not.toContain("Unknown");
    expect(within(rows[0]!).getByText("Context").tagName).toBe("DT");
    expect(
      within(dialog).getByText(
        "Use a model ID when configuring a route for this connection.",
      ),
    ).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Done" })).toBeTruthy();
    expect(within(dialog).queryByRole("checkbox")).toBeNull();
    expect(calls).toEqual([{ organizationId: "org-1", connectionId: "c1" }]);
  });

  it("filters locally by ID or name without changing the request, and clears filters", async () => {
    const user = userEvent.setup();
    const calls: Request[] = [];
    mount((request) => {
      calls.push(scope(request));
      return { models: sample };
    });
    const { dialog } = await open(user);
    await within(dialog).findAllByRole("listitem");
    const search = within(dialog).getByLabelText("Find a model");
    await user.type(search, "MODEL BETA");
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(1);
    await user.clear(search);
    await user.type(search, "GAMMA");
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(1);
    await user.clear(search);
    await user.type(search, "nothing-like-this");
    expect(within(dialog).getByText("No matching models")).toBeTruthy();
    expect(
      within(dialog).queryByText("No models returned for this connection"),
    ).toBeNull();
    await user.click(
      within(dialog).getByRole("button", { name: "Clear filters" }),
    );
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(4);
    expect((search as HTMLInputElement).value).toBe("");
    expect(calls).toHaveLength(1);
  });

  it("explains a successful empty discovery with a same-query Retry, even with a search", async () => {
    const user = userEvent.setup();
    const calls: Request[] = [];
    let models: unknown[] = [];
    mount((request) => {
      calls.push(scope(request));
      return { models };
    });
    const { dialog } = await open(user);
    expect(
      await within(dialog).findByText("No models returned for this connection"),
    ).toBeTruthy();
    await user.type(within(dialog).getByLabelText("Find a model"), "alpha");
    expect(
      within(dialog).getByText("No models returned for this connection"),
    ).toBeTruthy();
    expect(within(dialog).queryByText("No matching models")).toBeNull();
    models = sample.slice(0, 1);
    await user.click(within(dialog).getByRole("button", { name: "Retry" }));
    await within(dialog).findByText("model-alpha");
    expect(calls).toHaveLength(2);
    expect(calls[1]).toEqual(calls[0]);
  });

  it("announces loading accessibly", async () => {
    const user = userEvent.setup();
    let release: (value: object) => void = () => {};
    mount(
      () =>
        new Promise<object>((resolve) => {
          release = resolve;
        }),
    );
    const { dialog } = await open(user);
    const status = within(dialog).getByRole("status");
    expect(status.textContent).toContain("Loading models");
    expect(status.getAttribute("aria-busy")).toBe("true");
    release({ models: sample });
    await within(dialog).findByText("model-alpha");
  });

  it("shows a retryable error with its request ID and never an empty result", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    mount(() => {
      attempts += 1;
      if (attempts === 1)
        throw new ConnectError("secret detail", Code.Unavailable, undefined, [
          { desc: ErrorDetailSchema, value: { requestId: "req-4379" } },
        ]);
      return { models: sample };
    });
    const { dialog } = await open(user);
    expect(await within(dialog).findByText(/req-4379/)).toBeTruthy();
    expect(within(dialog).queryByText(/secret detail/)).toBeNull();
    expect(
      within(dialog).queryByText("No models returned for this connection"),
    ).toBeNull();
    expect(within(dialog).queryByText("No matching models")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Retry" }));
    expect(await within(dialog).findByText("model-alpha")).toBeTruthy();
    expect(within(dialog).queryByText(/req-4379/)).toBeNull();
  });

  it("does not imply fresh entitlement after a permission failure", async () => {
    const user = userEvent.setup();
    mount(() => {
      throw new ConnectError("no", Code.PermissionDenied);
    });
    const { dialog } = await open(user);
    expect(
      await within(dialog).findByText(/could not be confirmed/),
    ).toBeTruthy();
    expect(within(dialog).queryByRole("listitem")).toBeNull();
    expect(
      within(dialog).queryByText("No models returned for this connection"),
    ).toBeNull();
  });

  it("keeps loaded models under a stale notice when a refresh fails", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    mount(() => {
      attempts += 1;
      if (attempts > 1) throw new ConnectError("down", Code.Unavailable);
      return { models: sample };
    });
    const { dialog } = await open(user);
    await within(dialog).findByText("model-alpha");
    await user.click(await screen.findByRole("button", { name: "Done" }));
    await user.click(
      (await screen.findAllByRole("button", { name: /^View models/ }))[0]!,
    );
    const reopened = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(reopened).getByText(/last successful load/)).toBeTruthy(),
    );
    expect(within(reopened).getAllByRole("listitem")).toHaveLength(4);
  });

  it("warns that loaded models are stale when the browser goes offline after load", async () => {
    const user = userEvent.setup();
    const online = rs.spyOn(navigator, "onLine", "get");
    try {
      online.mockReturnValue(true);
      mount(() => ({ models: sample }));
      const { dialog } = await open(user);
      await within(dialog).findByText("model-alpha");
      expect(within(dialog).queryByText(/last successful load/)).toBeNull();
      online.mockReturnValue(false);
      act(() => {
        window.dispatchEvent(new Event("offline"));
      });
      expect(
        await within(dialog).findByText(/last successful load/),
      ).toBeTruthy();
      expect(within(dialog).getAllByRole("listitem")).toHaveLength(4);
      online.mockReturnValue(true);
      act(() => {
        window.dispatchEvent(new Event("online"));
      });
      await waitFor(() =>
        expect(within(dialog).queryByText(/last successful load/)).toBeNull(),
      );
    } finally {
      // React Query keeps its own online flag, so restore it for later tests.
      online.mockRestore();
      act(() => {
        window.dispatchEvent(new Event("online"));
      });
    }
  });

  it("warns when the dialog is reopened offline while the cached models are still fresh", async () => {
    const user = userEvent.setup();
    const online = rs.spyOn(navigator, "onLine", "get");
    try {
      online.mockReturnValue(true);
      const calls: Request[] = [];
      mount(
        (request) => {
          calls.push(scope(request));
          return { models: sample };
        },
        organization,
        15_000,
      );
      const { dialog } = await open(user);
      await within(dialog).findByText("model-alpha");
      await user.click(within(dialog).getByRole("button", { name: "Done" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      online.mockReturnValue(false);
      act(() => {
        window.dispatchEvent(new Event("offline"));
      });
      const { dialog: reopened } = await open(user);
      expect(
        await within(reopened).findByText(/last successful load/),
      ).toBeTruthy();
      expect(within(reopened).getAllByRole("listitem")).toHaveLength(4);
      // The cached query is still fresh, so reopening never refetched.
      expect(calls).toHaveLength(1);
    } finally {
      // React Query keeps its own online flag, so restore it for later tests.
      online.mockRestore();
      act(() => {
        window.dispatchEvent(new Event("online"));
      });
    }
  });

  it("dismisses with Esc, Close and Done and returns focus to the trigger", async () => {
    const user = userEvent.setup();
    mount(() => ({ models: sample }));
    for (const dismiss of ["escape", "Close dialog", "Done"]) {
      const { trigger, dialog } = await open(user, 1);
      await within(dialog).findByText("model-alpha");
      expect(
        within(dialog).getByRole("heading", {
          name: "Models from Home account",
        }),
      ).toBeTruthy();
      if (dismiss === "escape")
        fireEvent(dialog, new Event("cancel", { cancelable: true }));
      else
        await user.click(within(dialog).getByRole("button", { name: dismiss }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(document.activeElement).toBe(trigger);
    }
  });

  it("clears the dialog when the organization changes", async () => {
    const user = userEvent.setup();
    const calls: Request[] = [];
    const view = mount((request) => {
      calls.push(scope(request));
      return { models: sample };
    });
    const { dialog } = await open(user);
    await within(dialog).findByText("model-alpha");
    view.rerenderWith(other);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("model-alpha")).toBeNull();
    expect(calls.every((c) => c.organizationId === "org-1")).toBe(true);
  });
});
