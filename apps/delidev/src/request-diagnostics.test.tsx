import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  SubscriptionServiceIdentity, SessionService, SystemService, SystemCapability, newRequestId, RequestDiagnosticSchema,
  RequestDiagnosticSource as Source, RequestDiagnosticState as State, RequestDiagnosticOperation as Operation,
  ListRequestDiagnosticsResponseSchema,
} from "@delinoio/delidev-api-client";
import { RequestDiagnostics, validateDiagnosticPage } from "./request-diagnostics";

function fixture(supported = true) {
  const session = newRequestId(), execution = newRequestId(), id = newRequestId();
  const row = create(RequestDiagnosticSchema, { id, sessionId: session, executionId: execution, accountId: newRequestId(), connectionId: newRequestId(), providerId: newRequestId(), modelId: newRequestId(), revision: 9007199254740993n, source: Source.PROXY_HTTP, operation: Operation.RESPONSE, state: State.SUCCEEDED, purpose: "conversation", harness: "codex", correlationId: id, publicationRequestId: newRequestId(), httpAttempted: true, httpStatus: 200, durationMs: 0n, requestedEffort: "high", nativeResponseId: "resp_original", observedAt: "2026-09-30T00:00:00Z", finishedAt: "2026-09-30T00:00:01Z" });
  const read = vi.fn(async () => ({ records: [row], nextPageToken: "opaque-page" }));
  const transport = createRouterTransport((router) => { router.service(SystemService, { getStatus: () => ({ capabilities: supported ? [SystemCapability.REQUEST_DIAGNOSTICS_V1] : [] }) }); router.service(SessionService, { listRequestDiagnostics: read }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    const [open, setOpen] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}><input aria-label="Conversation draft" defaultValue="Retained draft" />{open ? <RequestDiagnostics sessionId={session} close={() => setOpen(false)} /> : null}</QueryClientProvider></TransportProvider>;
  }
  return { View, session, execution, row, read, client };
}

it("renders exact HTTP provenance and zero latency while absent observations remain unavailable", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByText("0 ms");
  expect(screen.getByText("resp_original")).toBeTruthy();
  expect(screen.getByText("high")).toBeTruthy();
  expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(2);
  expect(screen.getByText(f.row.accountId)).toBeTruthy();
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(document.activeElement).toBe(screen.getByLabelText("Execution ID (optional)"));
});

it("filters and paginates without changing the conversation draft, and disposes closed diagnostic reads", async () => {
  const f = fixture(); render(<f.View />); await screen.findByText("resp_original");
  fireEvent.change(screen.getByLabelText("Execution ID (optional)"), { target: { value: f.execution } });
  fireEvent.click(screen.getByRole("button", { name: "Apply execution filter" }));
  await waitFor(() => expect(f.read).toHaveBeenLastCalledWith(expect.objectContaining({ sessionId: f.session, executionId: f.execution, pageSize: 50 }), expect.anything()));
  fireEvent.click(screen.getByRole("button", { name: "Load more Model request diagnostics" }));
  await waitFor(() => expect(f.read).toHaveBeenLastCalledWith(expect.objectContaining({ pageToken: "opaque-page" }), expect.anything()));
  f.read.mockRejectedValueOnce(new ConnectError("PRIVATE_ERROR_SENTINEL", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh diagnostics" }));
  await screen.findByText("Refresh failed. The displayed observations may be stale.");
  expect(screen.queryByText("PRIVATE_ERROR_SENTINEL")).toBeNull();
  fireEvent.keyDown(screen.getByRole("complementary", { name: "Model request diagnostics" }), { key: "Escape" });
  expect(screen.queryByRole("complementary")).toBeNull();
  expect((screen.getByLabelText("Conversation draft") as HTMLInputElement).value).toBe("Retained draft");
  await waitFor(() => expect(f.client.getQueryCache().getAll().filter((query) => JSON.stringify(query.queryKey).toLowerCase().includes("listrequestdiagnostics"))).toHaveLength(0));
});

it("does not query unsupported servers", async () => {
  const f = fixture(false); render(<f.View />);
  await screen.findByText("Request diagnostics are unavailable on this server.");
  expect(f.read).not.toHaveBeenCalled();
});

it.each(["scope", "source", "setting", "path", "mixed", "counter", "harness", "lifecycle"])("rejects the entire malformed diagnostic page: %s", (kind) => {
  const f = fixture(), row = create(RequestDiagnosticSchema, f.row);
  if (kind === "scope") row.sessionId = newRequestId();
  if (kind === "source") row.source = 99 as Source;
  if (kind === "setting") row.effectiveEffort = "SECRET_SENTINEL";
  if (kind === "path") row.nativeResponseId = "/private/file";
  if (kind === "mixed") row.source = Source.NATIVE_INPUT;
  if (kind === "counter") row.durationMs = 18446744073709551615n;
  if (kind === "harness") row.harness = "/private/path";
  if (kind === "lifecycle") row.finishedAt = undefined;
  expect(() => validateDiagnosticPage(create(ListRequestDiagnosticsResponseSchema, { records: [row] }), f.session, "")).toThrow("unavailable");
});

it("preserves exact revision precision and native settings without inventing an HTTP attempt", () => {
  const f = fixture(), row = create(RequestDiagnosticSchema, { ...f.row, id: newRequestId(), source: Source.NATIVE_INPUT, operation: Operation.INPUT, inputId: newRequestId(), correlationId: "", nativeResponseId: "", httpAttempted: undefined, httpStatus: undefined, durationMs: undefined, nativeThreadId: newRequestId(), nativeTurnId: newRequestId() });
  row.nativeRequestId = row.id;
  const page = validateDiagnosticPage(create(ListRequestDiagnosticsResponseSchema, { records: [row] }), f.session, "");
  expect(page.records[0].revision).toBe(9007199254740993n);
  expect(page.records[0].httpAttempted).toBeUndefined();
});

it.each([
  ["2026-09-30T00:00:00.000999999Z", "2026-09-30T00:00:00.000000001Z", false],
  ["2026-09-30T00:00:00.000000001Z", "2026-09-30T00:00:00.000999999Z", true],
  ["2026-09-30T00:00:00.1Z", "2026-09-30T00:00:00.100000000Z", true],
  ["2026-09-30T00:00:00Z", "2026-09-30T00:00:00.000000001Z", true],
  ["2026-02-30T00:00:00Z", "2026-03-02T00:00:01Z", false],
])("compares validated UTC timestamps without losing nanoseconds: %s / %s", (observedAt, finishedAt, valid) => {
  const f = fixture(), row = create(RequestDiagnosticSchema, { ...f.row, observedAt, finishedAt });
  const validate = () => validateDiagnosticPage(create(ListRequestDiagnosticsResponseSchema, { records: [row] }), f.session, "");
  if (valid) expect(validate().records[0].observedAt).toBe(observedAt);
  else expect(validate).toThrow("unavailable");
});

// A single invalid retained row invalidates the complete page, including valid siblings.
it.each(["publication", "unsent-success", "statusless-success", "non-success-status"])("rejects diagnostic rows lacking original HTTP or receipt evidence: %s", (kind) => {
  const f = fixture(), row = create(RequestDiagnosticSchema, { ...f.row, id: newRequestId() });
  row.correlationId = row.id;
  if (kind === "publication") row.publicationRequestId = "";
  if (kind === "unsent-success") { row.httpAttempted = false; row.httpStatus = undefined; }
  if (kind === "statusless-success") row.httpStatus = undefined;
  if (kind === "non-success-status") row.httpStatus = 503;
  expect(() => validateDiagnosticPage(create(ListRequestDiagnosticsResponseSchema, { records: [f.row, row] }), f.session, "")).toThrow("unavailable");
});

it("preserves standard-only requested capacity independently from the effective tier", () => {
  const f = fixture(), row = create(RequestDiagnosticSchema, { ...f.row, requestedServiceTier: "standard_only", effectiveServiceTier: "standard" });
  const page = create(ListRequestDiagnosticsResponseSchema, { records: [row] });
  expect(validateDiagnosticPage(page, f.session, "").records[0].requestedServiceTier).toBe("standard_only");
  row.effectiveServiceTier = "standard_only";
  expect(() => validateDiagnosticPage(page, f.session, "")).toThrow("unavailable");
});

it("keeps native subscription service attribution independent from API providers", () => {
  const f = fixture(), row = create(RequestDiagnosticSchema, { ...f.row, id: newRequestId(), providerId: "", subscriptionService: SubscriptionServiceIdentity.CHATGPT, source: Source.NATIVE_INPUT, operation: Operation.INPUT, inputId: newRequestId(), correlationId: "", nativeResponseId: "", httpAttempted: undefined, httpStatus: undefined, durationMs: undefined, nativeThreadId: newRequestId(), nativeTurnId: newRequestId() });
  row.nativeRequestId = row.id;
  expect(validateDiagnosticPage(create(ListRequestDiagnosticsResponseSchema, { records: [row] }), f.session, "").records[0].providerId).toBe("");
  for (const wrong of [{ providerId: newRequestId() }, { subscriptionService: SubscriptionServiceIdentity.CLAUDE }, { subscriptionService: 99 as SubscriptionServiceIdentity }, { source: Source.PROXY_HTTP }]) {
    expect(() => validateDiagnosticPage(create(ListRequestDiagnosticsResponseSchema, { records: [create(RequestDiagnosticSchema, { ...row, ...wrong })] }), f.session, "")).toThrow("unavailable");
  }
});
