import { create } from "@bufbuild/protobuf";
import { StrictMode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, InboxService, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

function fixture() {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({ name: "Retained session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "paused", recovery: "none" }) });
  const message = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role: "assistant", text: '<script>window.invalid = true</script>', state: "completed" }) });
  const other = create(ResourceSchema, { ...session, id: newRequestId(), documentJson: encode({ name: "Other session", workspace: "general-chat", outcome: "idle", archive: "active", dispatch: "paused", recovery: "none" }) });
  other.sessionId = other.id;
  const enqueues = vi.fn(async () => ({ change: { session } }));
  const controls = vi.fn(async () => ({ change: { session } }));
  const status = vi.fn(async () => ({ version: "0.1.0", protocolVersion: 1 }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(SessionService, { listSessions: () => ({ sessions: [session, other] }), listQueue: () => ({ inputs: [] }), enqueueInput: enqueues, controlSession: controls });
    router.service(ResourceService, {
      getSnapshot: (request) => ({ resources: [request.filter?.sessionId === other.id ? other : session], cursor: "snapshot" }),
      listResources: (request) => ({ resources: request.filter?.kind === EntityKind.MESSAGE ? [message] : [] }),
      async *watchEvents(_request, context) {
        await new Promise<void>((resolve) => { if (context.signal.aborted) resolve(); else context.signal.addEventListener("abort", () => resolve(), { once: true }); });
      },
    });
    router.service(InboxService, { listInbox: () => ({ entries: [] }) });
    router.service(ConfigurationService, {});
  });
  return { transport, session, enqueues, controls, status };
}

it("keeps the draft and session mounted across settings and navigation, and renders native text inertly", async () => {
  const value = fixture();
  render(<StrictMode><App transport={value.transport} /></StrictMode>);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep my unsent input" } });
  fireEvent.click(screen.getByRole("button", { name: "Settings" }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close Settings" }));
  expect(window.document.activeElement).toBe(screen.getByRole("button", { name: "Settings" }));
  expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  await screen.findByText("No retained requests or completions.");
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Keep my unsent input");
  expect(await screen.findByText('<script>window.invalid = true</script>')).toBeTruthy();
  expect(window.document.querySelector("script")).toBeNull();
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("refreshes reads after recovery without replacing the connection's session draft", async () => {
  const value = fixture();
  value.status.mockRejectedValueOnce(new ConnectError("Server is stopped", Code.Unavailable));
  const view = render(<App transport={value.transport} connectionReady={false} />);
  await screen.findByText("Server unavailable");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Retain this across server restart" } });
  view.rerender(<App transport={value.transport} connectionReady connectionEpoch={1} />);
  await screen.findByText("Server 0.1.0");
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Retain this across server restart");
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("retries the exact accepted message identity after uncertainty instead of sending a new message", async () => {
  const value = fixture();
  value.enqueues.mockRejectedValueOnce(new ConnectError("Lost response.", Code.Unavailable));
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "One logical message" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Queue message" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same message" }));
  await waitFor(() => expect((composer as HTMLTextAreaElement).value).toBe(""));
  expect(value.enqueues).toHaveBeenCalledTimes(2);
  const calls = value.enqueues.mock.calls as unknown as [unknown][];
  expect(calls[0][0]).toEqual(calls[1][0]);
});

it("opens execution configuration without changing the unsent session draft or dispatching work", async () => {
  const value = fixture();
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep the original unsent draft" } });
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  expect(screen.getByText(/No accepted execution configuration/)).toBeTruthy();
  fireEvent.click(screen.getByText("Execution configuration and instructions"));
  expect((composer as HTMLTextAreaElement).value).toBe("Keep the original unsent draft");
  expect(value.controls).not.toHaveBeenCalled();
  expect(value.enqueues).not.toHaveBeenCalled();
});

it("sends explicit Restore with the original revision and never sends Resume on its behalf", async () => {
  const value = fixture();
  value.session.documentJson = encode({ name: "Retained session", workspace: "general-chat", outcome: "failed", archive: "archived", dispatch: "paused", recovery: "none" });
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const restore = await screen.findByRole("button", { name: "Restore" });
  await act(async () => fireEvent.click(restore));
  expect(value.controls).toHaveBeenCalledTimes(1);
  const calls = value.controls.mock.calls as unknown as [{ mutation: { id: string; expectedRevision: bigint }; action: number }][];
  expect(calls[0][0]).toMatchObject({ mutation: { id: value.session.id, expectedRevision: 7n }, action: 3 });
});

it("retains an uncertain message across a switch to another session", async () => {
  const value = fixture();
  value.enqueues.mockRejectedValueOnce(new ConnectError("Lost response.", Code.Unavailable));
  render(<App transport={value.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Message" }), { target: { value: "Original request" } });
  await waitFor(() => expect((screen.getByRole("button", { name: "Queue message" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await screen.findByRole("button", { name: "Retry the same message" });
  fireEvent.click(screen.getByRole("button", { name: /General Chat Other session/ }));
  await screen.findByRole("heading", { name: "Other session" });
  fireEvent.click(screen.getByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same message" }));
  await waitFor(() => expect(value.enqueues).toHaveBeenCalledTimes(2));
  const calls = value.enqueues.mock.calls as unknown as [unknown][];
  expect(calls[0][0]).toEqual(calls[1][0]);
});

it("drops connection-scoped drafts and caches when the selected transport changes", async () => {
  const first = fixture();
  const second = fixture();
  const view = render(<App transport={first.transport} />);
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  fireEvent.change(await screen.findByRole("textbox", { name: "Message" }), { target: { value: "Private draft for first server" } });
  view.rerender(<App transport={second.transport} />);
  await screen.findByText("Your sessions, in one place");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  expect((await screen.findByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("");
});


it("does not present cached server status as current connectivity after a failed refresh", async () => {
  const value = fixture();
  const view = render(<App transport={value.transport} />);
  await screen.findByText("Server 0.1.0");
  fireEvent.click(await screen.findByRole("button", { name: /General Chat Retained session/ }));
  const composer = await screen.findByRole("textbox", { name: "Message" });
  fireEvent.change(composer, { target: { value: "Keep while disconnected" } });
  value.status.mockRejectedValue(new ConnectError("Server disconnected", Code.Unavailable));
  view.rerender(<App transport={value.transport} connectionEpoch={1} />);
  await screen.findByText("Server unavailable");
  expect(screen.queryByText("Server 0.1.0")).toBeNull();
  expect((composer as HTMLTextAreaElement).value).toBe("Keep while disconnected");
  expect(value.enqueues).not.toHaveBeenCalled();
});
