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
import { RoutingPage } from "../src/routing";

const organization = create(OrganizationSchema, {
  id: "org-1",
  name: "Acme Studio",
  role: Role.MEMBER,
});
const route = (id: string, modelId: string) => ({
  id,
  modelId,
  enabled: true,
  targets: [],
});
type DeleteRequest = { organizationId?: string; routeId?: string };
type Fixture = {
  organization?: typeof organization;
  routes?: ReturnType<typeof route>[];
  deleteRoute?: (request: DeleteRequest) => object | Promise<object>;
};
function mount(fixture: Fixture) {
  const removed = new Set<string>();
  const cache = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((r: ConnectRouter) => {
    r.service(RoutingService, {
      listRoutes: () =>
        ({
          routes: (fixture.routes ?? [route("r1", "team-chat")]).filter(
            (item) => !removed.has(item.id),
          ),
        }) as never,
      deleteRoute: async (request) => {
        const result = (await fixture.deleteRoute?.(request)) ?? {};
        removed.add(request.routeId);
        return result;
      },
    });
    r.service(ProviderService, { listProviderConnections: () => ({}) });
    r.service(PoolService, { listPools: () => ({ pools: [] }) });
  });
  return render(
    <ApiProvider queryClient={cache} rpcTransport={transport}>
      <RoutingPage
        organization={fixture.organization ?? organization}
        session={undefined as never}
      />
    </ApiProvider>,
  );
}
async function openDelete(user: ReturnType<typeof userEvent.setup>, index = 0) {
  const trigger = (
    await screen.findAllByRole("button", { name: /^Delete route / })
  )[index]!;
  await user.click(trigger);
  return { trigger, dialog: screen.getByRole("dialog") };
}
const confirmButton = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", {
    name: /^(Delete route|Saving…)$/,
  }) as HTMLButtonElement;

describe("route delete confirmation", () => {
  it("renders the named dialog, alias summary and both consequence statements", async () => {
    const user = userEvent.setup();
    const deleteRoute = rs.fn((_request: DeleteRequest) => ({}));
    mount({ deleteRoute });
    const { dialog } = await openDelete(user);
    expect(screen.getByRole("dialog", { name: "Delete route?" })).toBe(dialog);
    expect(dialog.className).toContain("route-delete-dialog");
    expect(within(dialog).getByText("Only yours in Acme Studio.")).toBeTruthy();
    expect(within(dialog).getByText("Route alias")).toBeTruthy();
    expect(within(dialog).getByText("team-chat")).toBeTruthy();
    const consequence = within(dialog).getByText(
      "New requests for this alias can no longer use this route.",
    );
    const retained = within(dialog).getByText(
      "Provider connections and pools remain available.",
    );
    expect(consequence).not.toBe(retained);
    expect(
      within(dialog).getByRole("button", { name: "Close dialog" }),
    ).toBeTruthy();
    expect(within(dialog).queryByRole("textbox")).toBeNull();
    expect(within(dialog).queryByRole("link")).toBeNull();
    expect(
      within(dialog)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["×", "Cancel", "Delete route"]);
    expect(document.activeElement?.textContent).toBe("Cancel");
    expect(deleteRoute).not.toHaveBeenCalled();
  });

  it("describes alias, scope and consequence without nesting blocks in a paragraph", async () => {
    const user = userEvent.setup();
    mount({});
    const { dialog } = await openDelete(user);
    const ids = dialog.getAttribute("aria-describedby")!.split(" ");
    const text = ids.map((id) => document.getElementById(id)?.textContent);
    const description = text.join(" ");
    expect(description).toContain("team-chat");
    expect(description).toContain("Only yours in Acme Studio.");
    expect(description).toContain(
      "New requests for this alias can no longer use this route.",
    );
    expect(description).toContain(
      "Provider connections and pools remain available.",
    );
    for (const paragraph of dialog.querySelectorAll("p")) {
      expect(
        paragraph.querySelector("div,dl,dt,dd,p,ul,ol,section"),
      ).toBeNull();
    }
  });

  it("dismisses with Cancel, Close and Escape without a request and restores trigger focus", async () => {
    const user = userEvent.setup();
    const deleteRoute = rs.fn((_request: DeleteRequest) => ({}));
    mount({ deleteRoute });
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
    expect(deleteRoute).not.toHaveBeenCalled();
  });

  it("renders a long alias and organization name as full escaped text", async () => {
    const user = userEvent.setup();
    const alias = `<b>${"a".repeat(120)}</b>`;
    const longOrganization = create(OrganizationSchema, {
      id: "org-1",
      name: `Acme ${"Studio".repeat(20)} <i>x</i>`,
      role: Role.MEMBER,
    });
    mount({
      organization: longOrganization,
      routes: [route("r1", alias)],
    });
    const { dialog } = await openDelete(user);
    expect(within(dialog).getByText(alias).textContent).toBe(alias);
    expect(dialog.querySelector("b,i")).toBeNull();
    expect(
      within(dialog).getByText(`Only yours in ${longOrganization.name}.`),
    ).toBeTruthy();
  });

  it("deletes once with the selected scope, reports success after the server confirms and focuses the heading", async () => {
    const user = userEvent.setup();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    const deleteRoute = rs.fn(async (_request: DeleteRequest) => {
      await gate;
      return {};
    });
    mount({
      routes: [route("r1", "team-chat"), route("r2", "other")],
      deleteRoute,
    });
    const { dialog } = await openDelete(user, 1);
    expect(within(dialog).getByText("other")).toBeTruthy();
    const confirm = confirmButton(dialog);
    await user.click(confirm);
    await user.click(confirm);
    fireEvent.keyDown(confirm, { key: "Enter" });
    await waitFor(() => expect(confirm.disabled).toBe(true));
    expect(confirm.textContent).toBe("Saving…");
    expect(
      screen.getAllByRole("status").map((status) => status.textContent),
    ).toContain(
      "Saving… Closing this dialog does not cancel a submitted change.",
    );
    expect(screen.queryByText("Route deleted.")).toBeNull();
    release();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deleteRoute).toHaveBeenCalledOnce();
    expect(deleteRoute.mock.calls[0]![0]).toMatchObject({
      organizationId: "org-1",
      routeId: "r2",
    });
    await screen.findByText("Route deleted.");
    await waitFor(() => expect(screen.queryByText("other")).toBeNull());
    const heading = screen.getByRole("heading", {
      level: 1,
      name: "Your routing",
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(window.location.pathname).toBe("/");
  });

  it("keeps the submitted deletion running after dismissal and reports the final result on the page", async () => {
    const user = userEvent.setup();
    let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    const deleteRoute = rs.fn(async (_request: DeleteRequest) => {
      await gate;
      return {};
    });
    mount({ deleteRoute });
    const { dialog } = await openDelete(user);
    await user.click(confirmButton(dialog));
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("Route deleted.")).toBeNull();
    release();
    await screen.findByText("Route deleted.");
    expect(deleteRoute).toHaveBeenCalledOnce();
  });

  it("keeps the dialog, alias and scope after a failure and allows an explicit retry", async () => {
    const user = userEvent.setup();
    let attempts = 0;
    const deleteRoute = rs.fn((_request: DeleteRequest) => {
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
    mount({ deleteRoute });
    const { dialog } = await openDelete(user);
    await user.click(confirmButton(dialog));
    const alert = await within(dialog).findByRole("alert");
    expect(alert.textContent).toContain("req-123");
    expect(alert.textContent).not.toContain("database password leaked");
    expect(screen.queryByText("Route deleted.")).toBeNull();
    expect(within(dialog).getByText("team-chat")).toBeTruthy();
    expect(within(dialog).getByText("Only yours in Acme Studio.")).toBeTruthy();
    expect(confirmButton(dialog).disabled).toBe(false);
    await user.click(confirmButton(dialog));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deleteRoute).toHaveBeenCalledTimes(2);
    await screen.findByText("Route deleted.");
  });
});
