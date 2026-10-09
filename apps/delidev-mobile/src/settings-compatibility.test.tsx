// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { create, type Message } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type CreateSessionRequest, type SetNotificationPreferencesRequest, EntityKind, ResourceSchema, ResourceService, SystemService, SystemCapability, InboxService, NotificationPreferencesSchema, SituationNotificationPreferencesSchema } from "@delinoio/delidev-api-client";
import { NewSession, ServerNotificationSettings } from "./app";
import { documentBytes, documentOf, ProtectedState } from "./state";
import { en } from "./localization";
vi.mock("./platform", () => ({ storage: { read: async () => null, write: async () => {} } }));
afterEach(cleanup);

function resource(kind: EntityKind, id: string, document: Record<string, unknown>, schemaVersion = 1) {
  return create(ResourceSchema, { kind, id, revision: 1n, schemaVersion, documentJson: documentBytes(document) });
}
function fixture(defaultPlan = true, override = "inherit", modern = true, invalidGlobal = false) {
  const mutate = vi.fn(async (_request: Message) => {});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: modern ? [SystemCapability.SESSION_DEFAULTS_V1] : [] }) });
    router.service(ResourceService, {
      listResources: request => {
        const kind = request.filter?.kind ?? EntityKind.UNSPECIFIED;
        return { resources: kind === EntityKind.SETTINGS
          ? [resource(kind, "settings", { plan_mode_default: invalidGlobal ? "plan" : defaultPlan, automatic_plan_approval: false, branch_prefix: "delidev/" }, 3)]
          : [resource(kind, "choice", { name: "Saved choice", ...(kind === EntityKind.PROJECT ? { settings: { plan_mode_default: override } } : {}) }, kind === EntityKind.PROJECT ? 3 : 1)] };
      },
      getResource: request => ({ resource: resource(request.kind, request.id, { name: "Saved choice", settings: { plan_mode_default: override } }, 3) }),
    });
  });
  const view = (enabled = true) => <QueryClientProvider client={client}><TransportProvider transport={transport}><NewSession enabled={enabled} mutate={mutate} /></TransportProvider></QueryClientProvider>;
  return { client, mutate, view };
}
async function fill() {
  for (const label of [en.project, en.agent, en.runner]) {
    const select = await screen.findByLabelText(label);
    await waitFor(() => expect(select.querySelectorAll("option").length).toBe(2));
    fireEvent.change(select, { target: { value: "choice" } });
  }
  fireEvent.change(screen.getByLabelText(en.prompt), { target: { value: "Retained fixture prompt" } });
}
it.each([[true, "inherit", "plan"], [true, "disabled", "execute"], [false, "enabled", "plan"]])("resolves server %s and Project %s without manufacturing a manual mode", async (global, override, mode) => {
  const f = fixture(global, override); render(f.view()); await fill();
  await waitFor(() => expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe(mode));
  await waitFor(() => expect((screen.getByRole("button", { name: en.newSession }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: en.newSession }));
  await waitFor(() => expect(f.mutate).toHaveBeenCalledTimes(1));
  expect(documentOf((f.mutate.mock.calls[0][0] as CreateSessionRequest).documentJson).mode).toBe(mode);
});
it("preserves an explicit mode across refresh and a frozen pending presentation", async () => {
  const f = fixture(); const mounted = render(f.view()); await fill();
  await waitFor(() => expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("plan"));
  fireEvent.change(screen.getByLabelText(en.mode), { target: { value: "execute" } });
  await f.client.invalidateQueries();
  expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("execute");
  mounted.rerender(f.view(false));
  expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("execute");
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe("Retained fixture prompt");
  expect(f.mutate).not.toHaveBeenCalled();
});
it("keeps the original Execute default for an older server", async () => {
  const f = fixture(true, "inherit", false); render(f.view()); await fill();
  await waitFor(() => expect((screen.getByRole("button", { name: en.newSession }) as HTMLButtonElement).disabled).toBe(false));
  expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("execute");
});
it.each([true, false])("uses revision-bound granular writes only for a granular snapshot (%s)", async modern => {
  const state = new ProtectedState({ read: async () => null, write: async () => {} });
  vi.spyOn(state, "profile").mockReturnValue({ id: "profile", name: "Fixture", origin: "https://fixture.invalid", serverId: "server", deviceId: "device", token: "fixture" });
  const perform = vi.spyOn(state, "perform").mockResolvedValue(undefined);
  const preferences = create(NotificationPreferencesSchema, { revision: 7n, interactions: true, terminals: false, ...(modern ? { situations: create(SituationNotificationPreferencesSchema, {}) } : {}) });
  const reads: boolean[] = [];
  const transport = createRouterTransport(router => router.service(InboxService, { getNotificationPreferences: request => { reads.push(request.situations); return { preferences }; } }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><TransportProvider transport={transport}><ServerNotificationSettings state={state} id="profile" changed={() => {}} active /></TransportProvider></QueryClientProvider>);
  const control = await screen.findByLabelText(modern ? en.questions : en.interactions);
  await waitFor(() => expect((control as HTMLInputElement).disabled).toBe(false));
  expect(screen.getAllByRole("checkbox")).toHaveLength(modern ? 12 : 2);
  fireEvent.click(control);
  await waitFor(() => expect(perform).toHaveBeenCalledTimes(1));
  const request = perform.mock.calls[0][2] as SetNotificationPreferencesRequest;
  expect(reads.every(Boolean)).toBe(true);
  if (modern) { expect(request.expectedRevision).toBe(7n); expect(request.changes?.questions).toBe(true); expect(request.preferences).toBeUndefined(); }
  else { expect(request.preferences?.revision).toBe(7n); expect(request.changes).toBeUndefined(); }
});

it("ignores the Project override in General Chat and retains a captured automatic mode while disabled", async () => {
  const f = fixture(true, "disabled"); const mounted = render(f.view()); await fill();
  await waitFor(() => expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("execute"));
  fireEvent.change(screen.getByLabelText(en.workspace), { target: { value: "general-chat" } });
  await waitFor(() => expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("plan"));
  mounted.rerender(f.view(false));
  await f.client.invalidateQueries();
  expect((screen.getByLabelText(en.mode) as HTMLSelectElement).value).toBe("plan");
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe("Retained fixture prompt");
  expect(f.mutate).not.toHaveBeenCalled();
});
it("blocks malformed automatic defaults without dropping the draft or overriding an explicit choice", async () => {
  const f = fixture(true, "inherit", true, true); render(f.view()); await fill();
  await screen.findByText(en.defaultsUnavailable);
  expect((screen.getByRole("button", { name: en.newSession }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText(en.prompt) as HTMLTextAreaElement).value).toBe("Retained fixture prompt");
  expect(f.mutate).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText(en.mode), { target: { value: "execute" } });
  await waitFor(() => expect((screen.getByRole("button", { name: en.newSession }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByText(en.defaultsUnavailable)).toBeNull();
});
