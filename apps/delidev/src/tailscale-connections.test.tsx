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
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ErrorDetailSchema,
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
  const requests = document.createElement("div");
  document.body.append(target, requests);
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <MutationIntents>
          <TailscaleConnections
            target={target}
            requests={requests}
            onPaired={vi.fn()}
          />
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

const LOGIN_MESSAGE = "Tailscale login is required.";
const LOGIN_GUIDANCE =
  "Open Tailscale on this computer and sign in. Then refresh Tailscale devices and inspect the original access request.";

function unavailable(message: string, guidance: string) {
  return new ConnectError(message, Code.Unavailable, undefined, [
    { desc: ErrorDetailSchema, value: { code: "unavailable", guidance } },
  ]);
}

// Renders the Local access controls with a configurable setTailscaleAccess
// failure and a configurable discovery state.
function accessFixture(options: {
  failure: () => ConnectError;
  discoveryState?: string;
  accessReadFailure?: () => ConnectError;
}) {
  const set = vi.fn((_: { requestId: string; enabled: boolean }) => {
    throw options.failure();
  });
  const discover = vi.fn(() => ({
    documentJson: encode({
      state: options.discoveryState ?? "running",
      peers: [],
      observed_at: "2026-01-01T00:00:00Z",
    }),
  }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.TAILSCALE_CONNECTIONS_V1],
      }),
    });
    router.service(TailscaleService, {
      getTailscaleAccess: () => {
        if (options.accessReadFailure) throw options.accessReadFailure();
        return {
          documentJson: encode({
            enabled: false,
            state: "off",
            server_id: "s",
          }),
        };
      },
      listTailscaleApprovals: () => ({ documentJson: encode([]) }),
      discoverTailscale: discover,
      setTailscaleAccess: set,
    });
  });
  const current = document.createElement("div");
  const target = document.createElement("div");
  const requests = document.createElement("div");
  document.body.append(current, target, requests);
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <MutationIntents>
          <TailscaleConnections
            current={current}
            target={target}
            requests={requests}
            onPaired={vi.fn()}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { set, discover };
}

// The raw message is repeated inside the disclosure; only visible copy counts.
const outsideDisclosure = (text: string) =>
  screen.queryAllByText(text).filter((node) => !node.closest("details"));
const accessAlert = () =>
  document.querySelector<HTMLElement>(".tailscale-access [role='alert']");
async function enable() {
  fireEvent.click(await screen.findByRole("button", { name: "Enable access" }));
  const dialog = await screen.findByRole("dialog");
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Enable access" }),
  );
}

it("shows the localized login recovery outside the closed Technical details disclosure", async () => {
  const f = accessFixture({
    failure: () => unavailable(LOGIN_MESSAGE, LOGIN_GUIDANCE),
  });
  await enable();
  await waitFor(() => expect(outsideDisclosure(LOGIN_MESSAGE)).toHaveLength(1));
  const heading = outsideDisclosure(LOGIN_MESSAGE)[0];
  const alert = heading.closest('[role="alert"]') as HTMLElement;
  expect(alert.classList.contains("problem")).toBe(true);
  expect(outsideDisclosure(LOGIN_GUIDANCE)).toHaveLength(1);
  expect(alert.contains(outsideDisclosure(LOGIN_GUIDANCE)[0])).toBe(true);
  const details = alert.querySelector("details") as HTMLDetailsElement;
  expect(details.open).toBe(false);
  expect(details.textContent).toContain("unavailable");
  expect(details.textContent).toContain(LOGIN_MESSAGE);
  expect(
    within(alert).queryByText("DeliDev could not complete this request."),
  ).toBeNull();
  // Disclosure toggling and locale switching never send a mutation.
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect(outsideDisclosure("Tailscale 로그인이 필요합니다.")).toHaveLength(1);
  expect(
    outsideDisclosure(
      "이 컴퓨터에서 Tailscale을 열고 로그인하세요. Tailscale 기기 목록을 새로고침한 뒤 원래 접근 요청을 확인하세요.",
    ),
  ).toHaveLength(1);
  expect(outsideDisclosure(LOGIN_MESSAGE)).toHaveLength(0);
  expect(f.set).toHaveBeenCalledTimes(1);
});

it("retries the original login failure only through the existing Inspect control", async () => {
  const f = accessFixture({
    failure: () => unavailable(LOGIN_MESSAGE, LOGIN_GUIDANCE),
  });
  await enable();
  await waitFor(() => expect(outsideDisclosure(LOGIN_MESSAGE)).toHaveLength(1));
  const first = f.set.mock.calls[0][0];
  fireEvent.click(
    screen.getByRole("button", { name: "Inspect original request" }),
  );
  await waitFor(() => expect(f.set).toHaveBeenCalledTimes(2));
  expect(f.set.mock.calls[1][0]).toEqual(first);
  expect(f.discover).not.toHaveBeenCalledWith(
    expect.objectContaining({ enabled: expect.anything() }),
  );
});

it("keeps the generic Problem for a different unavailable failure", async () => {
  accessFixture({
    failure: () =>
      unavailable(
        "Tailscale is unavailable or its observation is unsupported.",
        "Inspect the installed Tailscale client and its permissions. Use manual connection or SSH setup when unavailable.",
      ),
  });
  await enable();
  await waitFor(() => expect(accessAlert()).not.toBeNull());
  expect(
    within(accessAlert()!).getByText(
      "DeliDev could not complete this request.",
    ),
  ).toBeTruthy();
  expect(outsideDisclosure(LOGIN_MESSAGE)).toHaveLength(0);
});

it("does not synthesize a login diagnosis from a logged-out discovery observation", async () => {
  accessFixture({
    failure: () =>
      unavailable(
        "Tailscale is unavailable or its observation is unsupported.",
        "Inspect the installed Tailscale client and its permissions. Use manual connection or SSH setup when unavailable.",
      ),
    discoveryState: "logged-out",
  });
  await enable();
  await waitFor(() => expect(accessAlert()).not.toBeNull());
  expect(
    within(accessAlert()!).getByText(
      "DeliDev could not complete this request.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("Tailscale login required")).toBeTruthy();
  expect(outsideDisclosure(LOGIN_MESSAGE)).toHaveLength(0);
});

it("ignores the login text when it comes from a read error or a different code", async () => {
  accessFixture({
    failure: () =>
      new ConnectError(LOGIN_MESSAGE, Code.Aborted, undefined, [
        {
          desc: ErrorDetailSchema,
          value: { code: "conflict", guidance: LOGIN_GUIDANCE },
        },
      ]),
    accessReadFailure: () => unavailable(LOGIN_MESSAGE, LOGIN_GUIDANCE),
  });
  await waitFor(() =>
    expect(document.querySelectorAll('[role="alert"]').length).toBeGreaterThan(
      0,
    ),
  );
  expect(outsideDisclosure(LOGIN_MESSAGE)).toHaveLength(0);
});

function readFixture(options: {
  list: () => { documentJson: Uint8Array };
  status?: () => { capabilities: SystemCapability[] };
}) {
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus:
        options.status ??
        (() => ({
          capabilities: [SystemCapability.TAILSCALE_CONNECTIONS_V1],
        })),
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
      listTailscaleConnections: options.list,
    });
  });
  const current = document.createElement("div");
  const target = document.createElement("div");
  const requests = document.createElement("div");
  document.body.append(current, target, requests);
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <MutationIntents>
          <TailscaleConnections
            current={current}
            target={target}
            requests={requests}
            onPaired={vi.fn()}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { current, requests };
}

it("reports an empty request list only after the list was read successfully", async () => {
  const { requests } = readFixture({
    list: () => ({ documentJson: encode([]) }),
  });
  await within(requests).findByText("No connection requests.");
  expect(
    within(requests).queryByText(/Connection requests could not be read/),
  ).toBeNull();
});

it("does not report a failed request list read as empty", async () => {
  const { requests } = readFixture({
    list: () => {
      throw new ConnectError("Unreachable", Code.Unavailable);
    },
  });
  await within(requests).findByText(
    "Connection requests could not be read. Refresh to try again.",
  );
  expect(within(requests).queryByText("No connection requests.")).toBeNull();
  expect(within(requests).getByRole("alert")).toBeTruthy();
});

it("does not report a failed capability read as an unsupported server", async () => {
  const { current } = readFixture({
    list: () => ({ documentJson: encode([]) }),
    status: () => {
      throw new ConnectError("Unreachable", Code.Unavailable);
    },
  });
  await within(current).findByText("Unavailable or blocked");
  expect(within(current).queryByText(/does not support Tailscale/)).toBeNull();
  expect(within(current).getByRole("alert")).toBeTruthy();
});
