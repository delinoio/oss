// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { TrackedJob } from "./jobs";
import { i18n } from "./localization";

function fixture(state = "queued") {
  const initial = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 871n, documentJson: encode({ state }) });
  let current = initial;
  const read = vi.fn(async () => ({ resource: current }));
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const children = vi.fn((_state: string, output: Record<string, unknown>) => <><label>Result draft<input defaultValue="Keep focus" /></label>{output.result === "ready" ? <button>Finish inspection</button> : null}</>);
  const view = <TransportProvider transport={transport}><QueryClientProvider client={client}><TrackedJob initial={initial} active>{children}</TrackedJob></QueryClientProvider></TransportProvider>;
  return { initial, read, client, children, view, state: (state: string) => { current = create(ResourceSchema, { ...initial, revision: initial.revision + 1n, documentJson: encode({ state, output: { result: "ready" } }) }); } };
}

it("observes the original job automatically and removes only generic success presentation", async () => {
  const f = fixture(); const view = render(f.view);
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(1));
  const input = screen.getByRole("textbox", { name: "Result draft" }); input.focus();
  expect(screen.queryByText(f.initial.id)).toBeNull();
  expect(screen.queryByText("871")).toBeNull();
  expect(screen.queryByRole("button", { name: /Refresh operation/ })).toBeNull();
  f.state("claimed"); await act(async () => { await f.client.invalidateQueries(); });
  f.state("succeeded");
  await waitFor(() => expect(view.container.querySelector(".notice")).toBeNull(), { timeout: 4000 });
  expect(screen.getByRole("button", { name: "Finish inspection" })).toBeTruthy();
  expect(view.container.querySelector(".notice")).toBeNull();
  expect(screen.queryByRole("region", { name: /Worker operation/ })).toBeNull();
  expect(document.activeElement).toBe(input);
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(document.activeElement).toBe(input);
  expect(view.container.querySelector(".notice")).toBeNull();
  await act(async () => { await i18n.changeLanguage("en"); });
  for (const [request] of f.read.mock.calls as unknown as [{ id: string }][]) expect(request.id).toBe(f.initial.id);
});

it.each(["failed", "canceled", "uncertain"])("keeps %s guidance without a generic title, UUID or refresh", async state => {
  const f = fixture(state); render(f.view);
  expect(await screen.findByRole("status")).toBeTruthy();
  expect(screen.queryByText(f.initial.id)).toBeNull();
  expect(screen.queryByRole("button", { name: /Refresh|Retry/ })).toBeNull();
  expect(screen.getByRole("textbox", { name: "Result draft" })).toHaveProperty("value", "Keep focus");
});

it("retries only a failed read of the original status without hiding feature children", async () => {
  const f = fixture("succeeded"); f.read.mockRejectedValueOnce(new ConnectError("Status read failed", Code.Unavailable)); render(f.view);
  const retry = await screen.findByRole("button", { name: "Retry original status read" });
  expect(screen.getByRole("textbox", { name: "Result draft" })).toBeTruthy();
  f.state("succeeded"); fireEvent.click(retry);
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry original status read" })).toBeNull());
  expect(f.read).toHaveBeenCalledTimes(2);
  for (const [request] of f.read.mock.calls as unknown as [{ id: string }][]) expect(request.id).toBe(f.initial.id);
});

it("retains the original identity when a status response is foreign", async () => {
  const f = fixture("succeeded");
  f.read.mockResolvedValueOnce({ resource: create(ResourceSchema, { ...f.initial, id: newRequestId() }) });
  render(f.view);
  expect(await screen.findByText("The original status could not be verified. Read it again before continuing.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry original status read" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry original status read" })).toBeNull());
  expect(f.read).toHaveBeenCalledTimes(2);
  for (const [request] of f.read.mock.calls as unknown as [{ id: string }][]) expect(request.id).toBe(f.initial.id);
});
