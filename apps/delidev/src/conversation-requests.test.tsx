// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRef } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InteractionService, ResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { Interaction } from "./interactions";
import { ConversationRequests } from "./conversation-requests";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { i18n } from "./localization";
const sessionId = newRequestId();
const question = { type: "user-question", closure: "open", questions: { questions: [{ id: "choice", header: "Original question", text: "Choose", options: [{ label: "Original answer" }], other: false, secret: false }] } };
const row = (data: Record<string, unknown> = question, revision = 9n, id = newRequestId()) => create(ResourceSchema, { id, sessionId, kind: EntityKind.INTERACTION, schemaVersion: 1, revision, documentJson: encode(data) });
function pages(rows: Resource[]) {
  return { data: { resources: rows, inputs: rows, nextPageToken: "" }, rows: rows.map(row => ({ id: row.id, revision: row.revision })), pages: [{ token: "", nextPageToken: "", rows: rows.map(row => ({ id: row.id, revision: row.revision })) }], payloadPages: [{ token: "", payload: rows }], nextPageToken: "", loaded: true, isPending: false, measure: vi.fn(), restore: vi.fn(), protect: vi.fn(), refresh: vi.fn(), retry: vi.fn(), reload: vi.fn(), append: vi.fn() } as unknown as Parameters<typeof ConversationRequests>[0]["query"];
}
function fixture(original = row()) {
  const answer = vi.fn(async (request: { mutation?: { requestId?: string } }) => ({ interaction: row({ ...question, response: { state: "queued", id: request.mutation?.requestId } }, 10n, original.id) }));
  const transport = createRouterTransport(router => router.service(InteractionService, { respondQuestion: answer }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const composer = createRef<HTMLTextAreaElement>();
  const query = pages([original]);
  const view = (overrides: Partial<Parameters<typeof ConversationRequests>[0]> = {}) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConversationRequests sessionId={sessionId} query={query} live={new Map()} removed={new Set()} arrivals={[]} active current composer={composer} drafts={new Map()} saveDraft={vi.fn()} {...overrides} /><textarea aria-label="Retained composer" ref={composer} defaultValue="Original draft" /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { original, answer, composer, query, view, transport, client };
}
it("retires an exact submitted form, then removes the verified empty tray without replacing the composer", async () => {
  const f = fixture(), mounted = render(f.view());
  const composer = screen.getByRole("textbox", { name: "Retained composer" });
  fireEvent.click(screen.getByRole("checkbox", { name: "Original answer" }));
  const submit = screen.getByRole("button", { name: "Send answers" }); submit.focus();
  fireEvent.click(submit);
  await screen.findByText("Response: queued");
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  expect(screen.queryByRole("checkbox", { name: "Original answer" })).toBeNull();
  expect(f.answer).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(document.activeElement).toBe(composer));
  const closed = row({ ...question, closure: "native-closed", response: { state: "accepted" } }, 11n, f.original.id);
  mounted.rerender(f.view({ live: new Map([[closed.id, closed]]) }));
  expect(mounted.container.querySelector("details.requests")).toBeNull();
  expect(screen.getByRole("textbox", { name: "Retained composer" })).toBe(composer);
  expect((composer as HTMLTextAreaElement).value).toBe("Original draft");
  mounted.rerender(f.view());
  expect(mounted.container.querySelector("details.requests")).toBeNull();
});
it("uses the same accepted projection for list refresh and restored payloads", () => {
  const f = fixture(), closed = row({ ...question, closure: "turn-ended", response: { state: "accepted" } }, 11n, f.original.id);
  const mounted = render(f.view({ query: pages([closed]) }));
  expect(mounted.container.querySelector("details.requests")).toBeNull();
  mounted.rerender(f.view({ query: { ...pages([closed]), payloadPages: [] } }));
  expect(mounted.container.querySelector("details.requests")).not.toBeNull();
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  mounted.rerender(f.view({ query: pages([f.original]) }));
  expect(mounted.container.querySelector("details.requests")).toBeNull();
});
it("keeps incomplete, failed and continuation reads reachable instead of claiming emptiness", () => {
  const f = fixture(), empty = pages([]), mounted = render(f.view({ query: { ...empty, data: undefined, loaded: false, isPending: true } }));
  expect(mounted.container.querySelector("details.requests")).not.toBeNull();
  mounted.rerender(f.view({ query: { ...empty, nextPageToken: "original-next" } }));
  expect(mounted.container.querySelector("details.requests")).not.toBeNull();
  mounted.rerender(f.view({ query: { ...empty, error: { failure: { code: "server_unavailable", message: "Read failed", guidance: "Retry the read." } } } as typeof empty }));
  expect(mounted.container.querySelector("details.requests")).not.toBeNull();
});
it("retains the exact uncertain receipt after closure and never retries automatically", async () => {
  const f = fixture(); f.answer.mockRejectedValueOnce(new ConnectError("Lost acknowledgement", Code.Unavailable));
  const mounted = render(f.view());
  fireEvent.click(screen.getByRole("checkbox", { name: "Original answer" })); fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  const closed = row({ ...question, closure: "turn-ended" }, 11n, f.original.id);
  mounted.rerender(f.view({ live: new Map([[closed.id, closed]]) }));
  expect(f.answer).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("checkbox", { name: "Original answer" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Retry the same answers" }));
  await waitFor(() => expect(f.answer).toHaveBeenCalledTimes(2));
  expect(f.answer.mock.calls[1][0]).toEqual(f.answer.mock.calls[0][0]);
});
it("retains an unrelated question and approval when the resolved question retires", () => {
  const f = fixture(), other = row(), approval = row({ type: "native-approval", closure: "open", approval: { harness: "codex", version: "unknown" } });
  const closed = row({ ...question, closure: "turn-ended" }, 11n, f.original.id);
  render(f.view({ query: pages([closed, other, approval]) }));
  expect(screen.getAllByRole("checkbox", { name: "Original answer" })).toHaveLength(1);
  expect(screen.getAllByRole("button", { name: "Send answers" })).toHaveLength(1);
  expect(screen.getByRole("heading", { name: "Native approval" })).toBeDefined();
});

it("keeps a removed row's original uncertain receipt reachable", async () => {
  const f = fixture(); f.answer.mockRejectedValueOnce(new ConnectError("Lost acknowledgement", Code.Unavailable));
  const mounted = render(f.view());
  fireEvent.click(screen.getByRole("checkbox", { name: "Original answer" })); fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same answers" });
  mounted.rerender(f.view({ removed: new Set([f.original.id]) }));
  expect(mounted.container.querySelector("details.requests")).not.toBeNull();
  expect(screen.getByRole("button", { name: "Retry the same answers" })).toBeDefined();
  expect(f.answer).toHaveBeenCalledTimes(1);
});
for (const width of [1200, 360]) it(`preserves composer focus and draft across language/reconnect at ${width}px`, async () => {
  const previousWidth = window.innerWidth;
  Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
  const f = fixture(), mounted = render(f.view());
  const composer = screen.getByRole("textbox", { name: "Retained composer" }); composer.focus();
  const closed = row({ ...question, closure: "native-closed" }, 11n, f.original.id);
  try {
    mounted.rerender(f.view({ live: new Map([[closed.id, closed]]) }));
    expect(document.activeElement).toBe(composer);
    await i18n.changeLanguage("ko");
    mounted.rerender(f.view({ current: false }));
    expect(screen.queryByRole("checkbox")).toBeNull();
    mounted.rerender(f.view());
    expect(mounted.container.querySelector("details.requests")).toBeNull();
    expect(composer).toBe(screen.getByRole("textbox", { name: "Retained composer" }));
    expect((composer as HTMLTextAreaElement).value).toBe("Original draft");
    expect(document.activeElement).toBe(composer);
  } finally { await i18n.changeLanguage("en"); Object.defineProperty(window, "innerWidth", { configurable: true, value: previousWidth }); }
});

it("keeps contradictory higher revisions visible without reviving answer authority", () => {
  const f = fixture(), queued = row({ ...question, response: { state: "queued", id: "original" } }, 10n, f.original.id);
  const mounted = render(f.view({ query: pages([queued]) }));
  const contradictory = row(question, 11n, f.original.id);
  mounted.rerender(f.view({ query: pages([contradictory]) }));
  expect(mounted.container.querySelector("details.requests")).not.toBeNull();
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(screen.queryByRole("button", { name: "Send answers" })).toBeNull();
  expect(screen.getByText("Response: unknown")).toBeDefined();
});
it("moves removed-control focus to the next original actionable request", () => {
  const f = fixture(), other = row(), mounted = render(f.view({ query: pages([f.original, other]) }));
  const controls = screen.getAllByRole("checkbox", { name: "Original answer" }); controls[0].focus();
  const closed = row({ ...question, closure: "turn-ended" }, 11n, f.original.id);
  mounted.rerender(f.view({ query: pages([f.original, other]), live: new Map([[closed.id, closed]]) }));
  expect(document.activeElement).toBe(screen.getByRole("checkbox", { name: "Original answer" }));
});

it("preserves retained question inspection outside the active tray", () => {
  const f = fixture(), closed = row({ ...question, closure: "native-closed", response: { state: "accepted" } }, 11n, f.original.id);
  render(<TransportProvider transport={f.transport}><QueryClientProvider client={f.client}><MutationIntents><Interaction resource={closed} refresh={vi.fn()} /></MutationIntents></QueryClientProvider></TransportProvider>);
  expect(screen.getByRole("checkbox", { name: "Original answer" })).toBeDefined();
  expect((screen.getByRole("button", { name: "Send answers" }) as HTMLButtonElement).disabled).toBe(true);
  expect(f.answer).not.toHaveBeenCalled();
});
