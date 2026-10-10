// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { createRouterTransport } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, ResourceService, SystemService } from "@delinoio/delidev-api-client";
import { App, NewSession } from "./app";
import { documentBytes, ProtectedState, uuid } from "./state";
vi.mock("./platform", () => ({ storage: { read: async () => null, write: async () => {} }, observeForeground: async () => () => {}, permission: async () => false }));
afterEach(cleanup);

it("keeps selector numbers and original option values after reorder without name-only reads", async () => {
  const ids = [uuid(), uuid(), uuid()], userName = uuid();
  let reverse = false;
  const reads: EntityKind[] = [];
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(ResourceService, {
      listResources: request => {
        const kind = request.filter?.kind ?? EntityKind.UNSPECIFIED;
        reads.push(kind);
        if (kind === EntityKind.SETTINGS) return { resources: [] };
        const resources = ids.map((id, index) => create(ResourceSchema, { id, kind, revision: 1n, schemaVersion: kind === EntityKind.PROJECT ? 3 : 1, documentJson: documentBytes({ name: index === 2 ? userName : index === 1 ? "Duplicate" : "" }) }));
        return { resources: reverse ? resources.reverse() : resources };
      },
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><TransportProvider transport={transport}><NewSession enabled mutate={async () => {}} /></TransportProvider></QueryClientProvider>);
  const project = await screen.findByLabelText("Project") as HTMLSelectElement;
  await waitFor(() => expect(project.options.length).toBe(4));
  const before = new Map([...project.options].map(option => [option.value, option.text]));
  expect(before.get(ids[0]!)).toMatch(/^Project number \d+$/);
  expect(before.get(ids[2]!)).toContain(userName);
  for (const id of ids) expect(before.get(id)).not.toContain(id);
  // The UUID-looking user name is separate from all resource IDs.
  fireEvent.change(project, { target: { value: ids[1] } });
  expect(project.value).toBe(ids[1]);
  reverse = true;
  await client.invalidateQueries();
  await waitFor(() => expect(project.options[1]!.value).toBe(ids[2]));
  for (const option of [...project.options]) expect(option.text).toBe(before.get(option.value));
  expect(project.value).toBe(ids[1]);
  expect(reads.every(kind => [EntityKind.SETTINGS, EntityKind.PROJECT, EntityKind.AGENT, EntityKind.MACHINE].includes(kind))).toBe(true);
  client.clear();
});

it.each(["en", "ko"])("keeps profile diagnostic presentation safe in %s while selection uses the original ID", async language => {
  const ids = [uuid(), uuid()], servers = [uuid(), uuid()], devices = [uuid(), uuid()];
  let raw = JSON.stringify({ version: 1, profiles: ids.map((id, index) => ({ id, name: "Same name", serverId: servers[index], deviceId: devices[index], token: "a".repeat(43), origin: "https://example.test" })), selectedProfile: "", language, theme: "system", notifications: false });
  const state = new ProtectedState({ read: async () => raw, write: async value => { raw = value; } });
  render(<App state={state} />);
  await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  fireEvent.click(screen.getByRole("button", { name: language === "ko" ? "설정" : "Settings" }));
  const profiles = screen.getAllByRole("button", { name: /Same name/ });
  expect(profiles).toHaveLength(2);
  expect(profiles[0]!.textContent).not.toBe(profiles[1]!.textContent);
  const retained = profiles[1]!.textContent;
  fireEvent.click(profiles[1]!);
  await waitFor(() => expect(state.state.selectedProfile).toBe(ids[1]));
  expect(screen.getAllByRole("button", { name: /Same name/ })[1]!.textContent).toBe(retained);
  for (const id of [...ids, ...servers, ...devices]) expect(document.body.textContent).not.toContain(id);
  const persisted = JSON.parse(raw);
  expect(persisted.selectedProfile).toBe(ids[1]);
  expect(persisted.profiles[1].serverId).toBe(servers[1]);
});
