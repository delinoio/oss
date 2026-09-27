import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InboxService, InboxViewSchema, InteractionService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { InboxSelection } from "./inbox-selection";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  const id = newRequestId(), sessionId = newRequestId(), source = newRequestId(), execution = newRequestId();
  let data = { archive: "archived", dispatch: "paused", recovery: "none", active_execution_id: execution, name: "Original selected session" };
  const entry = create(ResourceSchema, { id, sessionId, kind: EntityKind.INBOX, schemaVersion: 1, revision: 2n, documentJson: encode({ source: "interaction", source_id: source, read_state: "unread" }) });
  const interaction = create(ResourceSchema, { id: source, sessionId, kind: EntityKind.INTERACTION, schemaVersion: 1, revision: 3n, documentJson: encode({ type: "user-question", execution_id: execution, closure: "open", questions: { questions: [{ id: "original", text: "Preserved native question", other: true }] } }) });
  const view = () => create(InboxViewSchema, { entry, interaction, session: create(ResourceSchema, { id: sessionId, kind: EntityKind.SESSION, schemaVersion: 1, revision: 9n, documentJson: encode(data) }) });
  const get = vi.fn(() => ({ view: view() })), list = vi.fn(() => ({})), read = vi.fn(() => ({})), respond = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => { router.service(InboxService, { getInboxEntry: get, listInbox: list, setInboxReadState: read }); router.service(InteractionService, { respondQuestion: respond }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const element = <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><InboxSelection id={id} open={() => {}} close={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { element, get, list, read, respond, view, client, activate: () => { data = { ...data, archive: "active", dispatch: "claimed" }; } };
}

it("opens the exact current inbox source without reading, answering or reviving an archived request", async () => {
  const value = fixture(); render(value.element);
  await screen.findByText("Preserved native question");
  expect(value.get).toHaveBeenCalledTimes(1); expect(value.list).not.toHaveBeenCalled(); expect(value.read).not.toHaveBeenCalled(); expect(value.respond).not.toHaveBeenCalled();
  expect(screen.getByText(/session is paused, archived/)).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "Your answer" }).closest("fieldset[disabled]")).toBeTruthy();
  value.activate(); await act(() => value.client.invalidateQueries());
  await waitFor(() => expect(screen.getByRole("textbox", { name: "Your answer" }).closest("fieldset[disabled]")).toBeNull());
  value.get.mockImplementationOnce(() => { throw new ConnectError("Temporary failure", Code.Unavailable); });
  fireEvent.click(screen.getByRole("button", { name: "Refresh selected item" }));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(3));
  await waitFor(() => expect(screen.getByRole("textbox", { name: "Your answer" }).closest("fieldset[disabled]")).toBeTruthy());
  expect(value.read).not.toHaveBeenCalled(); expect(value.respond).not.toHaveBeenCalled();
});

it("refuses a joined source from another session instead of displaying its request", async () => {
  const value = fixture(), changed = value.view(); changed.interaction!.sessionId = newRequestId(); value.get.mockReturnValue({ view: changed });
  render(value.element);
  await screen.findByText(/This inbox item is unavailable/);
  expect(screen.queryByText("Preserved native question")).toBeNull(); expect(value.read).not.toHaveBeenCalled(); expect(value.respond).not.toHaveBeenCalled();
});
