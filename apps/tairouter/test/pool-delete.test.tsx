// SPDX-License-Identifier: Apache-2.0
import {
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
  OrganizationSchema,
  ErrorDetailSchema,
  Role,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { PoolsPage } from "../src/routing";

const organization = create(OrganizationSchema, {
  id: "org-1",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const pool = (id: string, name: string) => ({
  id,
  name,
  strategy: 1,
  members: [],
});
type DeleteRequest = { organizationId?: string; poolId?: string };
type Fixture = {
  organization?: typeof organization;
  pools?: ReturnType<typeof pool>[];
  deletePool?: (request: DeleteRequest) => object | Promise<object>;
  deleteRoute?: (request: unknown) => object;
  deleteProviderConnection?: (request: unknown) => object;
};
function mount(fixture: Fixture) {
  const removed = new Set<string>();
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(PoolService, {
      listPools: () =>
        ({
          pools: (fixture.pools ?? [pool("p1", "Team pool")]).filter(
            (item) => !removed.has(item.id),
          ),
        }) as never,
      deletePool: async (request) => {
        const result = (await fixture.deletePool?.(request)) ?? {};
        removed.add(request.poolId);
        return result;
      },
    });
    r.service(ProviderService, {
      listProviderConnections: () => ({}),
      deleteProviderConnection: (request) =>
        (fixture.deleteProviderConnection?.(request) ?? {}) as never,
    });
    r.service(RoutingService, {
      listRoutes: () => ({}),
      deleteRoute: (request) => (fixture.deleteRoute?.(request) ?? {}) as never,
    });
  });
  const element = (org: typeof organization) => (
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <PoolsPage organization={org} session={undefined as never} />
    </ApiProvider>
  );
  const view = render(element(fixture.organization ?? organization));
  return {
    switchOrganization: (next: typeof organization) =>
      view.rerender(element(next)),
  };
}
async function openDelete(user: ReturnType<typeof userEvent.setup>, index = 0) {
  const trigger = (await screen.findAllByRole("button", { name: "Delete" }))[
    index
  ]!;
  await user.click(trigger);
  return { trigger, dialog: screen.getByRole("dialog") };
}
const confirmButton = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", {
    name: /^(Delete pool|Saving…)$/,
  }) as HTMLButtonElement;

describe("pool delete confirmation", () => {
  it("renders the named dialog, context list, separate consequences and the scoped routing link", async () => {
    const user = userEvent.setup();
    const deletePool = rs.fn((_request: DeleteRequest) => ({}));
    mount({ deletePool });
    const { dialog } = await openDelete(user);
    expect(screen.getByRole("dialog", { name: "Delete Team pool?" })).toBe(
      dialog,
    );
    expect(dialog.className).toContain("pool-delete-dialog");
    expect(within(dialog).getByText("Only yours in Acme Studio.")).toBeTruthy();
    const terms = Array.from(dialog.querySelectorAll("dt")).map(
      (item) => item.textContent,
    );
    const values = Array.from(dialog.querySelectorAll("dd")).map(
      (item) => item.textContent,
    );
    expect(terms).toEqual(["Pool", "Organization"]);
    expect(values).toEqual(["Team pool", "Acme Studio"]);
    const connections = within(dialog).getByText(
      "Connections remain available.",
    );
    const repair = within(dialog).getByText(
      "Routes using this pool must be repaired before future requests can use them.",
    );
    expect(connections).not.toBe(repair);
    expect(connections.tagName).toBe("P");
    expect(repair.tagName).toBe("P");
    const link = within(dialog).getByRole("link", { name: "Review routing" });
    expect(link.getAttribute("href")).toBe("/organizations/org-1/routing");
    expect(link.getAttribute("target")).toBeNull();
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    expect(within(dialog).queryByRole("textbox")).toBeNull();
    expect(
      within(dialog)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["×", "Cancel", "Delete pool"]);
    expect(document.activeElement?.textContent).toBe("Cancel");
    expect(deletePool).not.toHaveBeenCalled();
  });

  it("encodes the organization ID in the routing link", async () => {
    const user = userEvent.setup();
    mount({
      organization: create(OrganizationSchema, {
        id: "org/1&x=y",
        name: "Acme Studio",
        role: Role.MEMBER,
      }),
    });
    const { dialog } = await openDelete(user);
    expect(
      within(dialog)
        .getByRole("link", { name: "Review routing" })
        .getAttribute("href"),
    ).toBe(`/organizations/${encodeURIComponent("org/1&x=y")}/routing`);
  });

  it("is described by the consequences and the scope line without nesting blocks in a paragraph", async () => {
    const user = userEvent.setup();
    mount({});
    const { dialog } = await openDelete(user);
    const ids = dialog.getAttribute("aria-describedby")!.split(" ");
    const description = ids
      .map((id) => document.getElementById(id)?.textContent)
      .join(" ");
    expect(description).toContain("Connections remain available.");
    expect(description).toContain(
      "Routes using this pool must be repaired before future requests can use them.",
    );
    expect(description).toContain("Only yours in Acme Studio.");
    expect(description).not.toContain("Team pool");
    for (const paragraph of dialog.querySelectorAll("p")) {
      expect(
        paragraph.querySelector("div,dl,dt,dd,p,ul,ol,section,a"),
      ).toBeNull();
    }
  });

  it("dismisses with Cancel, Close and Escape without a request and restores trigger focus", async () => {
    const user = userEvent.setup();
    const deletePool = rs.fn((_request: DeleteRequest) => ({}));
    mount({ deletePool });
    const { trigger, dialog } = await openDelete(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    await user.click(trigger);
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Close dialog",
      }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    await user.click(trigger);
    expect(document.activeElement?.textContent).toBe("Cancel");
    fireEvent(
      screen.getByRole("dialog"),
      new Event("cancel", { cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(deletePool).not.toHaveBeenCalled();
  });

  it("does not mount a dialog before a pool is selected", async () => {
    mount({});
    await screen.findByRole("heading", { name: "Team pool" });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("renders long pool and organization names as full escaped text", async () => {
    const user = userEvent.setup();
    const name = `<b>${"p".repeat(120)}</b>`;
    const longOrganization = create(OrganizationSchema, {
      id: "org-1",
      name: `Acme ${"Studio".repeat(20)} <i>x</i>`,
      role: Role.MEMBER,
    });
    mount({ organization: longOrganization, pools: [pool("p1", name)] });
    const { dialog } = await openDelete(user);
    expect(dialog.querySelector("b,i")).toBeNull();
    expect(screen.getByRole("dialog", { name: `Delete ${name}?` })).toBe(
      dialog,
    );
    expect(dialog.querySelectorAll("dd")[0]!.textContent).toBe(name);
    expect(dialog.querySelectorAll("dd")[1]!.textContent).toBe(
      longOrganization.name,
    );
    expect(
      within(dialog).getByText(`Only yours in ${longOrganization.name}.`),
    ).toBeTruthy();
  });

  it("deletes once with the selected scope, reports success only after confirmation and focuses the heading", async () => {
    const user = userEvent.setup();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    const deletePool = rs.fn(async (_request: DeleteRequest) => {
      await gate;
      return {};
    });
    const deleteRoute = rs.fn((_request: unknown) => ({}));
    const deleteProviderConnection = rs.fn((_request: unknown) => ({}));
    mount({
      pools: [pool("p1", "Team pool"), pool("p2", "Other pool")],
      deletePool,
      deleteRoute,
      deleteProviderConnection,
    });
    const { dialog } = await openDelete(user, 1);
    expect(dialog.querySelectorAll("dd")[0]!.textContent).toBe("Other pool");
    const confirm = confirmButton(dialog);
    await user.click(confirm);
    await user.click(confirm);
    fireEvent.keyDown(confirm, { key: "Enter" });
    await waitFor(() => expect(confirm.disabled).toBe(true));
    expect(confirm.textContent).toBe("Saving…");
    // The modal has its own polite status, separate from the inert page status.
    const status = within(dialog).getByRole("status");
    expect(status.textContent).toBe("Saving…");
    expect(status.getAttribute("aria-live")).toBeNull();
    expect(
      screen.getAllByRole("status").map((item) => item.textContent),
    ).toContain(
      "Saving… Closing this dialog does not cancel a submitted change.",
    );
    expect(screen.queryByText("Pool deleted.")).toBeNull();
    release();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deletePool).toHaveBeenCalledOnce();
    expect(deletePool.mock.calls[0]![0]).toMatchObject({
      organizationId: "org-1",
      poolId: "p2",
    });
    expect(deleteRoute).not.toHaveBeenCalled();
    expect(deleteProviderConnection).not.toHaveBeenCalled();
    await screen.findByText("Pool deleted.");
    await waitFor(() => expect(screen.queryByText("Other pool")).toBeNull());
    const heading = screen.getByRole("heading", {
      level: 1,
      name: "Account pools",
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(window.location.pathname).toBe("/");
  });

  it("keeps the submitted deletion running after dismissal and reports the final result on the page", async () => {
    const user = userEvent.setup();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    const deletePool = rs.fn(async (_request: DeleteRequest) => {
      await gate;
      return {};
    });
    mount({ deletePool });
    const { dialog } = await openDelete(user);
    await user.click(confirmButton(dialog));
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("Pool deleted.")).toBeNull();
    release();
    await screen.findByText("Pool deleted.");
    expect(deletePool).toHaveBeenCalledOnce();
  });

  it("keeps the dialog, pool and scope after a failure and allows an explicit retry", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    const deletePool = rs.fn((_request: DeleteRequest) => {
      attempts += 1;
      if (attempts === 1)
        throw new ConnectError(
          "database password leaked",
          Code.Unavailable,
          undefined,
          [
            {
              desc: ErrorDetailSchema,
              value: create(ErrorDetailSchema, { requestId: "req-123" }),
            },
          ],
        );
      return {};
    });
    mount({ deletePool });
    const { dialog } = await openDelete(user);
    await user.click(confirmButton(dialog));
    const alert = await within(dialog).findByRole("alert");
    expect(alert.textContent).toContain("req-123");
    expect(alert.textContent).not.toContain("database password leaked");
    expect(screen.queryByText("Pool deleted.")).toBeNull();
    expect(dialog.querySelectorAll("dd")[0]!.textContent).toBe("Team pool");
    expect(within(dialog).getByText("Only yours in Acme Studio.")).toBeTruthy();
    expect(confirmButton(dialog).disabled).toBe(false);
    expect(within(dialog).getByRole("status").textContent).toBe("");
    await user.click(confirmButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deletePool).toHaveBeenCalledTimes(2);
    await screen.findByText("Pool deleted.");
  });

  it("closes when the organization changes and never replays the mutation", async () => {
    const user = userEvent.setup();
    const deletePool = rs.fn((_request: DeleteRequest) => ({}));
    const view = mount({ deletePool });
    await openDelete(user);
    view.switchOrganization(
      create(OrganizationSchema, {
        id: "org-2",
        name: "Other Org",
        role: Role.MEMBER,
      }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("Other Org", { selector: "dd" })).toBeNull();
    expect(deletePool).not.toHaveBeenCalled();
  });
});
