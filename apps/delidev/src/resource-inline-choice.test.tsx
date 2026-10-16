// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
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
import { afterEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  ResourceChoice,
  ResourceChoicePresentation,
} from "./configuration-fields";
import { encode } from "./documents";

function projects(names: string[]) {
  return names.map((name) =>
    create(ResourceSchema, {
      kind: EntityKind.PROJECT,
      schemaVersion: 1,
      id: newRequestId(),
      revision: 1n,
      documentJson: encode({ name, repositories: [] }),
    }),
  );
}

function fixture(
  rows: ReturnType<typeof projects>,
  pages: ReturnType<typeof projects>[] = [rows],
) {
  const list = vi.fn(async (request: { filter?: { pageToken: string } }) => {
    const index = request.filter?.pageToken
      ? Number(request.filter.pageToken)
      : 0;
    return {
      resources: pages[index] ?? [],
      nextPageToken: index + 1 < pages.length ? String(index + 1) : "",
    };
  });
  const get = vi.fn(async (request: { id: string }) => ({
    resource: [...rows, ...pages.flat()].find((row) => row.id === request.id),
  }));
  const transport = createRouterTransport((router) =>
    router.service(ResourceService, { listResources: list, getResource: get }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const change = vi.fn(),
    invoke = vi.fn();
  const view = (value = "", disabled = false, active = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <ResourceChoice
          label="Project"
          kind={EntityKind.PROJECT}
          value={value}
          active={active}
          disabled={disabled}
          change={change}
          showStatus
          presentation={ResourceChoicePresentation.Inline}
          emptyLabel="General Chat"
          inlineAction={{ label: "New project", invoke }}
        />
      </QueryClientProvider>
    </TransportProvider>
  );
  return { rows, list, get, change, invoke, view };
}

const names = (list: HTMLElement) =>
  within(list)
    .getAllByRole("option")
    .map((option) => option.textContent);

afterEach(() => {
  vi.restoreAllMocks();
});

it("shows General Chat and every loaded project without opening a popup", async () => {
  const value = fixture(projects(["oss", "swc-next"]));
  render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(3));
  expect(names(list)).toEqual(["General Chat", "oss", "swc-next"]);
  expect(screen.queryByRole("combobox")).toBeNull();
  // Exactly one accepted row is selected; the empty identity is General Chat.
  expect(
    within(list)
      .getAllByRole("option")
      .filter((option) => option.getAttribute("aria-selected") === "true")
      .map((option) => option.textContent),
  ).toEqual(["General Chat"]);
  expect(list.dataset.value).toBe("");
});

it("accepts a fresh exact-ID selection once and ignores the accepted identity", async () => {
  const value = fixture(projects(["oss", "swc-next"]));
  const view = render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(3));
  fireEvent.click(within(list).getByRole("option", { name: "swc-next" }));
  await waitFor(() => expect(value.change).toHaveBeenCalledTimes(1));
  expect(value.change.mock.calls[0][0]).toBe(value.rows[1].id);
  expect(value.get).toHaveBeenCalledWith(
    expect.objectContaining({ id: value.rows[1].id }),
    expect.anything(),
  );
  // The owner has not accepted the callback yet, so the old identity remains.
  expect(list.dataset.value).toBe("");
  view.rerender(value.view(value.rows[1].id));
  expect(
    within(list)
      .getByRole("option", { name: "swc-next" })
      .getAttribute("aria-selected"),
  ).toBe("true");
  // Let the accepted identity's own metadata read settle before counting.
  await act(async () => {});
  const reads = value.get.mock.calls.length;
  fireEvent.click(within(list).getByRole("option", { name: "swc-next" }));
  fireEvent.keyDown(within(list).getByRole("option", { name: "swc-next" }), {
    key: "Enter",
  });
  await act(async () => {});
  expect(value.change).toHaveBeenCalledTimes(1);
  expect(value.get.mock.calls.length).toBe(reads);
  // Choosing General Chat reports the empty identity without a resource read.
  fireEvent.click(within(list).getByRole("option", { name: "General Chat" }));
  await waitFor(() => expect(value.change).toHaveBeenCalledTimes(2));
  expect(value.change.mock.calls[1][0]).toBe("");
});

it("moves focus with arrows, Home and End, selects with Enter and Space, and lets Tab leave", async () => {
  const value = fixture(projects(["a", "b", "c"]));
  render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(4));
  const option = (name: string) => within(list).getByRole("option", { name });
  expect(option("General Chat").tabIndex).toBe(0);
  expect(option("a").tabIndex).toBe(-1);
  option("General Chat").focus();
  fireEvent.keyDown(option("General Chat"), { key: "ArrowDown" });
  expect(document.activeElement).toBe(option("a"));
  expect(option("a").tabIndex).toBe(0);
  fireEvent.keyDown(option("a"), { key: "End" });
  expect(document.activeElement).toBe(option("c"));
  fireEvent.keyDown(option("c"), { key: "ArrowDown" });
  expect(document.activeElement).toBe(option("c"));
  fireEvent.keyDown(option("c"), { key: "ArrowUp" });
  expect(document.activeElement).toBe(option("b"));
  fireEvent.keyDown(option("b"), { key: "Home" });
  expect(document.activeElement).toBe(option("General Chat"));
  // Tab is never captured, so focus can continue to later controls.
  expect(fireEvent.keyDown(option("General Chat"), { key: "Tab" })).toBe(true);
  fireEvent.keyDown(option("General Chat"), { key: "ArrowDown" });
  fireEvent.keyDown(option("a"), { key: "Enter" });
  await waitFor(() => expect(value.change).toHaveBeenCalledTimes(1));
  expect(value.change.mock.calls[0][0]).toBe(value.rows[0].id);
  fireEvent.keyDown(option("b"), { key: " " });
  await waitFor(() => expect(value.change).toHaveBeenCalledTimes(2));
  expect(value.change.mock.calls[1][0]).toBe(value.rows[1].id);
});

it("sizes the viewport to five rendered rows and grows wrapped rows", async () => {
  // Layout is unavailable in jsdom: model 44px rows where the second wraps.
  const row = (node: HTMLElement) =>
    [...(node.parentElement?.children ?? [])].indexOf(node);
  const height = (node: HTMLElement) =>
    node.getAttribute("role") === "option" && row(node) === 1 ? 88 : 44;
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(
    function (this: HTMLElement) {
      return this.getAttribute("role") === "option" ? height(this) : 0;
    },
  );
  vi.spyOn(HTMLElement.prototype, "offsetTop", "get").mockImplementation(
    function (this: HTMLElement) {
      if (this.getAttribute("role") !== "option") return 0;
      let top = 0;
      for (let index = 0; index < row(this); index++)
        top += index === 1 ? 88 : 44;
      return top;
    },
  );
  const value = fixture(projects(["one", "two", "three", "four", "five"]));
  render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(6));
  const viewport = list.parentElement!;
  // General Chat + four projects are visible: 44 + 88 + 44 + 44 + 44.
  await waitFor(() => expect(viewport.style.maxHeight).toBe("264px"));
  // The sixth row stays reachable inside the same scroll owner.
  expect(
    viewport.contains(within(list).getByRole("option", { name: "five" })),
  ).toBe(true);
});

it("keeps natural height for five rows or fewer", async () => {
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(44);
  const value = fixture(projects(["one", "two", "three", "four"]));
  render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(5));
  expect(list.parentElement!.style.maxHeight).toBe("");
  // The stylesheet's five-row cap is lifted for short lists so a wrapped row
  // never pushes the last row behind a scrollbar.
  expect(list.parentElement!.hasAttribute("data-short")).toBe(true);
});

it("restores the scroll cap once a short list grows past five rows", async () => {
  const value = fixture(projects(["one", "two", "three", "four", "five"]), [
    projects(["one", "two", "three", "four"]),
    projects(["five", "six"]),
  ]);
  render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(5));
  expect(list.parentElement!.hasAttribute("data-short")).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Load more Project" }));
  await waitFor(() => expect(names(list)).toHaveLength(7));
  expect(list.parentElement!.hasAttribute("data-short")).toBe(false);
});

it("keeps an off-page accepted identity selected, loads more pages and retries failures", async () => {
  const [first, second, retained] = projects(["First", "Second", "Retained"]);
  const value = fixture([first, second, retained], [[first], [second]]);
  value.list.mockImplementationOnce(async () => ({
    resources: [first],
    nextPageToken: "1",
  }));
  render(value.view(retained.id));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toContain("First"));
  // Retained is outside the reached pages but is the single selected row.
  await waitFor(() =>
    expect(
      within(list)
        .getAllByRole("option")
        .filter((option) => option.getAttribute("aria-selected") === "true")
        .map((option) => option.textContent),
    ).toEqual(["Retained"]),
  );
  expect(list.dataset.value).toBe(retained.id);
  value.list.mockRejectedValueOnce(new ConnectError("down", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Load more Project" }));
  const retry = await screen.findByRole("button", { name: "Retry" });
  expect(names(list)).toContain("First");
  fireEvent.click(retry);
  await within(list).findByRole("option", { name: "Second" });
  expect(value.change).not.toHaveBeenCalled();
});

it("keeps an unresolved accepted identity selected while its exact read fails", async () => {
  const value = fixture(projects(["First"]));
  const missing = newRequestId();
  value.get.mockRejectedValue(
    new ConnectError("denied", Code.PermissionDenied),
  );
  render(value.view(missing));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toContain("First"));
  await act(async () => {});
  const selected = within(list)
    .getAllByRole("option")
    .filter((option) => option.getAttribute("aria-selected") === "true");
  expect(selected).toHaveLength(1);
  expect(selected[0].textContent).toContain(
    "is outside this page or unavailable",
  );
  expect(names(list)[1]).toBe(selected[0].textContent);
  expect(list.dataset.value).toBe(missing);
  // Choosing the retained identity again is a no-op.
  fireEvent.click(selected[0]);
  await act(async () => {});
  expect(value.change).not.toHaveBeenCalled();
});

it("shows General Chat, an empty status and the add action when there are no projects", async () => {
  const value = fixture([]);
  render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  expect(names(list)).toEqual(["General Chat"]);
  await screen.findByText("No selectable Project choices are on this page.");
  const add = screen.getByRole("button", { name: "New project" });
  expect(add.getAttribute("title")).toBe("New project");
  fireEvent.click(add);
  expect(value.invoke).toHaveBeenCalledOnce();
  expect(document.activeElement).toBe(add);
});

it("blocks selection while locked or inactive but keeps the identity visible", async () => {
  const value = fixture(projects(["oss", "swc-next"]));
  const view = render(value.view(""));
  const list = screen.getByRole("listbox", { name: "Project" });
  await waitFor(() => expect(names(list)).toHaveLength(3));
  view.rerender(value.view("", true));
  expect(list.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(within(list).getByRole("option", { name: "oss" }));
  fireEvent.keyDown(within(list).getByRole("option", { name: "oss" }), {
    key: "Enter",
  });
  await act(async () => {});
  expect(value.change).not.toHaveBeenCalled();
  expect(list.dataset.value).toBe("");
  // Navigation keys are not consumed, move no focus and keep the tab stop.
  const general = within(list).getByRole("option", { name: "General Chat" });
  general.focus();
  for (const key of ["ArrowDown", "ArrowUp", "Home", "End"])
    expect(fireEvent.keyDown(general, { key })).toBe(true);
  expect(document.activeElement).toBe(general);
  expect(general.tabIndex).toBe(0);
  expect(within(list).getByRole("option", { name: "oss" }).tabIndex).toBe(-1);
  view.rerender(value.view("", false, false));
  expect(screen.getByRole("button", { name: "New project" })).toHaveProperty(
    "disabled",
    true,
  );
});
