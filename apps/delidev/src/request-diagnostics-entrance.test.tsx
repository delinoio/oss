// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  RequestDiagnosticSchema,
  RequestDiagnosticOperation as Operation,
  RequestDiagnosticSource as Source,
  RequestDiagnosticState as State,
  SessionService,
  SystemCapability,
  SystemService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { RequestDiagnostics } from "./request-diagnostics";
import {
  dispatchAnimation,
  setAnimation,
  stubReducedMotion,
} from "./session-tool-entrance-helpers";

function fixture() {
  const session = newRequestId(),
    id = newRequestId();
  const row = create(RequestDiagnosticSchema, {
    id,
    sessionId: session,
    executionId: newRequestId(),
    accountId: newRequestId(),
    connectionId: newRequestId(),
    providerId: newRequestId(),
    model: { nativeId: "exact-original-native" },
    revision: 9n,
    source: Source.PROXY_HTTP,
    operation: Operation.RESPONSE,
    state: State.SUCCEEDED,
    purpose: "conversation",
    harness: "codex",
    correlationId: id,
    publicationRequestId: newRequestId(),
    httpAttempted: true,
    httpStatus: 200,
    durationMs: 0n,
    nativeResponseId: "resp_original",
    observedAt: "2026-09-30T00:00:00Z",
    finishedAt: "2026-09-30T00:00:01Z",
  });
  row.model!.providerId = row.providerId;
  // A held reread resolves late, after the panel was closed.
  let release: (() => void) | undefined;
  let hold = false;
  const read = vi.fn(async () => {
    if (hold)
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    return { records: [row], nextPageToken: "opaque-page" };
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.REQUEST_DIAGNOSTICS_V1],
      }),
    });
    router.service(SessionService, { listRequestDiagnostics: read });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = (open = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <input aria-label="Conversation draft" defaultValue="Retained draft" />
        {open ? <RequestDiagnostics sessionId={session} close={() => {}} /> : null}
      </QueryClientProvider>
    </TransportProvider>
  );
  return {
    view,
    read,
    client,
    hold: () => {
      hold = true;
    },
    release: () => release?.(),
  };
}

const header = () =>
  document.querySelector<HTMLElement>("aside.session-files > header")!;

beforeEach(() => {
  stubReducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("animates only the header once per open and never replays for reads", async () => {
  const f = fixture();
  const mounted = render(f.view());
  await screen.findByText("resp_original");
  const node = header();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  // Neither the panel, its scroll root nor the body carry the attribute.
  expect(document.querySelectorAll("[data-entrance]").length).toBe(1);
  expect(node.closest("[data-entrance]")).toBe(node);
  // The execution input keeps the focus that diagnostics grants on mount.
  expect(document.activeElement).toBe(
    screen.getByLabelText("Execution ID (optional)"),
  );
  const reads = f.read.mock.calls.length;
  fireEvent.click(
    screen.getByRole("button", { name: "Load more Model request diagnostics" }),
  );
  await waitFor(() => expect(f.read.mock.calls.length).toBeGreaterThan(reads));
  mounted.rerender(f.view());
  expect(header()).toBe(node);
  dispatchAnimation(node);
  expect(header().getAttribute("data-entrance")).toBeNull();
  await f.client.invalidateQueries({ refetchType: "active" });
  expect(header().getAttribute("data-entrance")).toBeNull();
});

it("plays once per open, and a late reply after leaving cannot replay or focus", async () => {
  const f = fixture();
  const mounted = render(f.view());
  await screen.findByText("resp_original");
  dispatchAnimation(header());
  f.hold();
  void f.client.invalidateQueries({ refetchType: "active" });
  await waitFor(() => expect(f.read.mock.calls.length).toBeGreaterThan(1));
  const reads = f.read.mock.calls.length;
  mounted.rerender(f.view(false));
  expect(document.querySelector("aside.session-files")).toBeNull();
  f.release();
  await Promise.resolve();
  expect(f.read.mock.calls.length).toBe(reads);
  expect(document.querySelector("[data-entrance]")).toBeNull();
  expect(document.activeElement).not.toBe(
    document.querySelector("input[aria-label='Execution ID (optional)']"),
  );
  // A new explicit open is a new activation.
  mounted.rerender(f.view(true));
  await screen.findByLabelText("Execution ID (optional)");
  expect(header().getAttribute("data-entrance")).toBe("y2");
});

it("is suppressed by Animation Off and reduced motion and never replays on re-enable", async () => {
  const change = stubReducedMotion(false);
  const f = fixture();
  render(f.view());
  await screen.findByText("resp_original");
  expect(header().getAttribute("data-entrance")).toBe("y2");
  await setAnimation("false");
  expect(header().getAttribute("data-entrance")).toBeNull();
  await setAnimation(undefined);
  expect(header().getAttribute("data-entrance")).toBeNull();
  // Reduced motion mid-flight is also cleared.
  const second = fixture();
  render(second.view());
  await waitFor(() =>
    expect(
      document.querySelectorAll("aside.session-files > header[data-entrance]")
        .length,
    ).toBe(1),
  );
  change(true);
  await waitFor(() =>
    expect(
      document.querySelectorAll("aside.session-files > header[data-entrance]")
        .length,
    ).toBe(0),
  );
  change(false);
  expect(
    document.querySelectorAll("aside.session-files > header[data-entrance]")
      .length,
  ).toBe(0);
});
