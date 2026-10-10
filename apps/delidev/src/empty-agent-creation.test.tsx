// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";
import { i18n } from "./localization";

const agent = () => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.AGENT, revision: 1n, schemaVersion: 4, documentJson: encode({ name: "Original Agent", harness: "codex", routes: [], templates: [], options: { permission: "default" } }) });
type Page = { resources: Resource[]; nextPageToken?: string };
function fixture(read: () => Page | Promise<Page>, allowed?: string[], selected = "") {
  const navigate = vi.fn(), change = vi.fn(), list = vi.fn(read);
  const retained = selected ? create(ResourceSchema, { ...agent(), id: selected }) : undefined;
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: list, getResource: () => ({ resource: retained }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={selected} change={change} active={active} allowed={allowed} showStatus required createEmptyAgent={navigate} /></QueryClientProvider></TransportProvider>;
  const rendered = render(view());
  return { client, navigate, change, list, rendered, view };
}
afterEach(async () => { await i18n.changeLanguage("en"); });

it.each(["en", "ko"])("offers only an explicit localized creation action after complete empty evidence in %s", async language => {
  await i18n.changeLanguage(language);
  const value = fixture(() => ({ resources: [] }));
  const button = await screen.findByRole("button", { name: language === "ko" ? "에이전트 워커 생성" : "Create agent worker" });
  expect(button.getAttribute("type")).toBe("button");
  expect(screen.queryByRole("combobox", { name: "Agent Worker" })).toBeNull();
  expect(button.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  button.focus(); expect(document.activeElement).toBe(button);
  fireEvent.click(button);
  expect(value.navigate).toHaveBeenCalledTimes(1);
  expect(value.change).not.toHaveBeenCalled();
  expect(value.list).toHaveBeenCalledTimes(1);
});

it.each([undefined, []])("does not infer absence from project-filtered rows (%s)", async allowed => {
  const row = agent();
  const value = fixture(() => ({ resources: [row] }), allowed ?? [newRequestId()]);
  await waitFor(() => expect(value.list).toHaveBeenCalledTimes(1));
  expect(await screen.findByText("The project restrictions exclude the Agent Workers on this page.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(screen.queryByText("No selectable Agent Worker choices are on this page.")).toBeNull();
});

it("preserves continuation and selected off-page identity without inferring absence", async () => {
  const selected = newRequestId();
  const value = fixture(() => ({ resources: [], nextPageToken: "next-original" }), undefined, selected);
  await waitFor(() => expect(value.list).toHaveBeenCalledTimes(1));
  const choice = screen.getByRole("combobox", { name: "Agent Worker" });
  await waitFor(() => expect(choice.dataset.value).toBe(selected));
  fireEvent.click(choice);
  expect(await screen.findByRole("button", { name: /Load more/ })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  expect(value.change).not.toHaveBeenCalled();
});

it.each([Code.PermissionDenied, Code.Unavailable, Code.DataLoss])("keeps failed reads distinct from absence (%s)", async code => {
  fixture(() => { throw new ConnectError("Original read failed", code); });
  expect(await screen.findByRole("button", { name: "Retry current read" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
});

it("rejects malformed rows rather than projecting filtered absence", async () => {
  fixture(() => ({ resources: [create(ResourceSchema, { ...agent(), kind: EntityKind.MACHINE })] }), []);
  expect(await screen.findByRole("button", { name: "Retry current read" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
});

it("removes the absence action during refresh and restores ordinary choices", async () => {
  let release!: (page: Page) => void;
  let reading: Page | Promise<Page> = { resources: [] };
  const value = fixture(() => reading);
  await screen.findByRole("button", { name: "Create agent worker" });
  reading = new Promise<Page>(resolve => { release = resolve; });
  let refresh!: Promise<void>;
  act(() => { refresh = value.client.invalidateQueries(); });
  await waitFor(() => expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull());
  const row = agent();
  await act(async () => { release({ resources: [row] }); await refresh; });
  expect(await screen.findByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  expect(value.change).not.toHaveBeenCalled();
  expect(value.navigate).not.toHaveBeenCalled();
});

it("retains ordinary inventory and never selects a new worker after return", async () => {
  let rows: Resource[] = [];
  const value = fixture(() => ({ resources: rows }));
  const originalButton = await screen.findByRole("button", { name: "Create agent worker" });
  fireEvent.click(originalButton);
  value.rendered.rerender(value.view(false));
  fireEvent.click(originalButton);
  expect(value.navigate).toHaveBeenCalledTimes(1);
  rows = [agent()];
  value.rendered.rerender(value.view(true));
  const choice = await screen.findByRole("combobox", { name: "Agent Worker" });
  await waitFor(() => expect(value.list.mock.calls.length).toBeGreaterThan(1));
  expect(choice.dataset.value).toBe("");
  expect(value.change).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
});


it("does not reuse stale empty evidence after a failed refresh", async () => {
  let failed = false;
  const value = fixture(() => { if (failed) throw new ConnectError("Original refresh failed", Code.Unavailable); return { resources: [] }; });
  await screen.findByRole("button", { name: "Create agent worker" });
  failed = true;
  await act(async () => { await value.client.invalidateQueries(); });
  await screen.findByRole("button", { name: "Retry current read" });
  expect(screen.queryByRole("button", { name: "Create agent worker" })).toBeNull();
  expect(screen.getByRole("combobox", { name: "Agent Worker" })).toBeTruthy();
  expect(value.change).not.toHaveBeenCalled();
});
