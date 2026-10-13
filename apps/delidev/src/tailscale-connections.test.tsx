// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  SystemCapability,
  SystemService,
  TailscaleService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { TailscaleConnections } from "./tailscale-connections";

afterEach(async () => {
  cleanup();
  document.body.replaceChildren();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
});

enum Outcome {
  Lost = "lost",
  Accepted = "accepted",
}

function approval(id: string) {
  return {
    id,
    name: "Fixture device",
    server_id: "server",
    origin: "https://device.example",
    role: "client",
    code: "123456",
    state: "pending",
  };
}

function fixture(first: Outcome) {
  const id = newRequestId();
  const seen = { resume: [] as string[], cancel: [] as string[] };
  const respond = (calls: string[], requestId: string) => {
    calls.push(requestId);
    if (calls.length === 1 && first === Outcome.Lost)
      throw new ConnectError("Lost response", Code.Unavailable);
    return { documentJson: encode({ ...approval(id), state: "denied" }) };
  };
  const resume = vi.fn((request: { requestId: string }) =>
    respond(seen.resume, request.requestId),
  );
  const cancel = vi.fn((request: { requestId: string }) =>
    respond(seen.cancel, request.requestId),
  );
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.TAILSCALE_CONNECTIONS_V1],
      }),
    });
    router.service(TailscaleService, {
      getTailscaleAccess: () => ({
        documentJson: encode({ enabled: false, state: "off", server_id: "s" }),
      }),
      listTailscaleApprovals: () => ({ documentJson: encode([]) }),
      discoverTailscale: () => ({
        documentJson: encode({
          state: "running",
          peers: [],
          observed_at: "2026-01-01T00:00:00Z",
        }),
      }),
      listTailscaleConnections: () => ({
        documentJson: encode([approval(id)]),
      }),
      resumeTailscaleConnection: resume,
      cancelTailscaleConnection: cancel,
    });
  });
  const target = document.createElement("div");
  document.body.append(target);
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <MutationIntents>
          <TailscaleConnections target={target} onPaired={vi.fn()} />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { id, resume, cancel, seen };
}

const rowResume = () =>
  screen.findByRole("button", { name: "Inspect original request" });
const rowCancel = () =>
  screen.findByRole("button", { name: "Cancel original request" });

it("retries an uncertain resume with the exact original request and never double-sends from the row", async () => {
  const f = fixture(Outcome.Lost);
  const row = await rowResume();
  fireEvent.click(row);
  const retry = await screen.findByRole("button", {
    name: "Retry the original resume request",
  });
  expect(f.resume).toHaveBeenCalledTimes(1);
  expect((row as HTMLButtonElement).disabled).toBe(true);
  expect(row.getAttribute("aria-describedby")).toBeTruthy();
  expect(
    document.getElementById(row.getAttribute("aria-describedby")!)?.textContent,
  ).toMatch(/original request/);
  fireEvent.click(row);
  expect(f.resume).toHaveBeenCalledTimes(1);
  // The cancel row action is independent of the retained resume mutation.
  expect(((await rowCancel()) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(retry);
  await waitFor(() => expect(f.resume).toHaveBeenCalledTimes(2));
  expect(f.seen.resume).toEqual([f.id, f.id]);
  expect(f.cancel).not.toHaveBeenCalled();
  await waitFor(() =>
    expect(
      screen.queryByRole("button", {
        name: "Retry the original resume request",
      }),
    ).toBeNull(),
  );
});

it("retries an uncertain cancel with the exact original request and never double-sends from the row", async () => {
  const f = fixture(Outcome.Lost);
  const row = await rowCancel();
  fireEvent.click(row);
  const retry = await screen.findByRole("button", {
    name: "Retry the original cancel request",
  });
  expect(f.cancel).toHaveBeenCalledTimes(1);
  expect((row as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(row);
  expect(f.cancel).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Retry the original resume request" }),
  ).toBeNull();
  fireEvent.click(retry);
  await waitFor(() => expect(f.cancel).toHaveBeenCalledTimes(2));
  expect(f.seen.cancel).toEqual([f.id, f.id]);
  expect(f.resume).not.toHaveBeenCalled();
});

it("localizes the uncertain resume retry control in Korean", async () => {
  const f = fixture(Outcome.Lost);
  fireEvent.click(await rowResume());
  const retry = await screen.findByRole("button", {
    name: "Retry the original resume request",
  });
  expect(retry.tagName).toBe("BUTTON");
  expect(retry.getAttribute("type")).toBe("button");
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect(
    screen.getByRole("button", { name: "원래 재개 요청 다시 시도" }),
  ).toBeTruthy();
  expect(f.resume).toHaveBeenCalledTimes(1);
});

it("does not show retry controls after an accepted resume", async () => {
  const f = fixture(Outcome.Accepted);
  fireEvent.click(await rowResume());
  await waitFor(() => expect(f.resume).toHaveBeenCalledTimes(1));
  expect(
    screen.queryByRole("button", { name: "Retry the original resume request" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Retry the original cancel request" }),
  ).toBeNull();
});
