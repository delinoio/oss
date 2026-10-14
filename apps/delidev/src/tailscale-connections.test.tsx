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
  // 1-based read count from which the read fails (after earlier successes).
  accessReadFailFrom?: number;
  // The access document served before the failing read (default: ready).
  accessBeforeFailure?: Record<string, unknown>;
  // The 1-based read after which reads succeed again (default: never).
  accessReadFailUntil?: number;
  // Holds every access read until this promise settles.
  accessReadGate?: Promise<void>;
}) {
  let reads = 0;
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
      getTailscaleAccess: async () => {
        await options.accessReadGate;
        reads += 1;
        if (
          options.accessReadFailure &&
          reads >= (options.accessReadFailFrom ?? 1) &&
          reads <= (options.accessReadFailUntil ?? Infinity)
        )
          throw options.accessReadFailure();
        return {
          documentJson: encode(
            options.accessReadFailFrom
              ? (options.accessBeforeFailure ?? {
                  enabled: true,
                  state: "ready",
                  server_id: "s",
                })
              : { enabled: false, state: "off", server_id: "s" },
          ),
        };
      },
      listTailscaleApprovals: () => ({ documentJson: encode([]) }),
      discoverTailscale: discover,
      setTailscaleAccess: set,
    });
  });
  const current = document.createElement("div");
  const target = document.createElement("div");
  document.body.append(current, target);
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
  // The action stays disabled until the first access read establishes the state.
  const button = await screen.findByRole("button", { name: "Enable access" });
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(button);
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

// Check-only causes (issue 3687): a proven cause shows localized guidance
// outside Technical details; everything else keeps the generic Problem.
const DEVICE = "Fixture peer";
const CORRELATION = newRequestId();
function checkFailure(
  message: string,
  code: string,
  cause: string,
  connectCode = Code.Unavailable,
) {
  return new ConnectError(message, connectCode, undefined, [
    {
      desc: ErrorDetailSchema,
      value: {
        code,
        cause,
        guidance: "Original safe guidance.",
        correlationId: CORRELATION,
      },
    },
  ]);
}
function checkFixture(options: {
  failure?: () => ConnectError;
  // 1-based check attempt from which the failure applies (default: the first).
  failFrom?: number;
  // 1-based discovery read from which discovery fails (default: never).
  discoveryFailFrom?: number;
  peerOverrides?: Record<string, unknown>;
  // Peer fields for a 1-based discovery read, merged over peerOverrides.
  peerAtDiscovery?: (discovery: number) => Record<string, unknown>;
  // The failure applies only while the 1-based check attempt is at most this.
  failUntil?: number;
}) {
  let attempts = 0;
  let discoveries = 0;
  const check = vi.fn(() => {
    attempts += 1;
    if (
      options.failure &&
      attempts >= (options.failFrom ?? 1) &&
      attempts <= (options.failUntil ?? Infinity)
    )
      throw options.failure();
    return {
      documentJson: encode({
        version: 1,
        server_id: "server",
        origin: "https://peer.example:8443",
        approval: true,
      }),
    };
  });
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
      discoverTailscale: () => {
        discoveries += 1;
        if (
          options.discoveryFailFrom &&
          discoveries >= options.discoveryFailFrom
        )
          throw new ConnectError("Discovery failed", Code.Unavailable);
        return {
          documentJson: encode({
            state: "running",
            peers: [
              {
                id: "peer-1",
                name: DEVICE,
                host: "peer.example",
                ownership: "own",
                online: true,
                shared_to: false,
                ...options.peerOverrides,
                ...options.peerAtDiscovery?.(discoveries),
              },
            ],
            observed_at: "2026-01-01T00:00:00Z",
          }),
        };
      },
      checkTailscale: check,
    });
  });
  const target = document.createElement("div");
  document.body.append(target);
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <MutationIntents>
          <TailscaleConnections target={target} onPaired={vi.fn()} />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return { check, attempts: () => attempts, discoveries: () => discoveries };
}
async function runCheck() {
  fireEvent.click(await screen.findByRole("button", { name: "Check" }));
}
const rowAlert = () =>
  document.querySelector<HTMLElement>(".tailscale-list [role='alert']");

it("shows the proven connection refusal outside Technical details without claiming the process is stopped", async () => {
  const f = checkFixture({
    failure: () =>
      checkFailure(
        "Tailscale is unavailable or its observation is unsupported.",
        "unavailable",
        "connection_refused",
      ),
  });
  await runCheck();
  const heading = await screen.findByText(
    `Could not connect to DeliDev on ${DEVICE}.`,
  );
  const alert = heading.closest('[role="alert"]') as HTMLElement;
  expect(heading.closest("details")).toBeNull();
  expect(
    within(alert)
      .getByText(
        `On ${DEVICE}, start DeliDev and enable Tailscale access, then select Check again.`,
      )
      .closest("details"),
  ).toBeNull();
  expect(screen.getByText("DeliDev: Could not connect")).toBeTruthy();
  expect(alert.textContent).not.toMatch(/stopped|not running/i);
  // The original safe failure remains inside the closed disclosure.
  const details = alert.querySelector("details") as HTMLDetailsElement;
  expect(details.open).toBe(false);
  expect(details.textContent).toContain(
    "Tailscale is unavailable or its observation is unsupported.",
  );
  expect(details.textContent).toContain("Original safe guidance.");
  expect(details.textContent).toContain("unavailable");
  expect(details.textContent).toContain(CORRELATION);
  expect(
    within(alert).queryByText("DeliDev could not complete this request."),
  ).toBeNull();
  // No automatic retry happened.
  expect(f.attempts()).toBe(1);
});

it("re-translates the visible cause guidance and keeps the original technical details", async () => {
  checkFixture({
    failure: () =>
      checkFailure(
        "Original safe message.",
        "unavailable",
        "connection_refused",
      ),
  });
  await runCheck();
  await screen.findByText(`Could not connect to DeliDev on ${DEVICE}.`);
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect(
    screen.getByText(`${DEVICE}의 DeliDev에 연결할 수 없습니다.`),
  ).toBeTruthy();
  expect(
    screen.getByText(
      `${DEVICE}에서 DeliDev를 시작하고 Tailscale 접근을 켠 다음 확인을 다시 선택하세요.`,
    ),
  ).toBeTruthy();
  expect(screen.getByText("DeliDev: 연결할 수 없음")).toBeTruthy();
  expect(rowAlert()!.querySelector("details")!.textContent).toContain(
    "Original safe message.",
  );
});

const CAUSES = [
  {
    cause: "timeout",
    code: "unavailable",
    heading: `DeliDev on ${DEVICE} did not respond in time.`,
    korean: `${DEVICE}의 DeliDev가 제시간에 응답하지 않았습니다.`,
  },
  {
    cause: "certificate_invalid",
    code: "unavailable",
    heading: `The secure connection to ${DEVICE} could not be verified.`,
    korean: `${DEVICE}와의 보안 연결을 확인할 수 없습니다.`,
  },
  {
    cause: "unexpected_http",
    code: "unavailable",
    heading: `${DEVICE} replied, but not as DeliDev.`,
    korean: `${DEVICE}가 응답했지만 DeliDev가 아닙니다.`,
  },
  {
    cause: "invalid_identity",
    code: "unsupported",
    heading: `DeliDev on ${DEVICE} is not compatible.`,
    korean: `${DEVICE}의 DeliDev와 호환되지 않습니다.`,
  },
];
for (const entry of CAUSES) {
  it(`shows only the matching guidance for ${entry.cause}`, async () => {
    checkFixture({
      failure: () =>
        checkFailure(
          "Original safe message.",
          entry.code,
          entry.cause,
          entry.code === "unsupported" ? Code.Unimplemented : Code.Unavailable,
        ),
    });
    await runCheck();
    const heading = await screen.findByText(entry.heading);
    expect(heading.closest("details")).toBeNull();
    // Other causes' guidance and the refused row text never appear.
    expect(
      screen.queryByText(`Could not connect to DeliDev on ${DEVICE}.`),
    ).toBeNull();
    expect(screen.queryByText("DeliDev: Could not connect")).toBeNull();
    expect(screen.getByText("DeliDev: Unavailable or blocked")).toBeTruthy();
    expect(rowAlert()!.querySelectorAll("strong")).toHaveLength(
      // Cause heading plus the disclosure's original message.
      2,
    );
    await act(async () => {
      await i18n.changeLanguage("ko");
    });
    expect(screen.getByText(entry.korean)).toBeTruthy();
  });
}

it("keeps the generic Problem for unproven, unknown or mismatched causes", async () => {
  for (const failure of [
    () => checkFailure("Original safe message.", "unavailable", ""),
    () => checkFailure("Original safe message.", "unavailable", "dns_failure"),
    () =>
      checkFailure(
        "Original safe message.",
        "unsupported",
        "connection_refused",
        Code.Unimplemented,
      ),
    () =>
      checkFailure("Original safe message.", "unavailable", "invalid_identity"),
  ]) {
    checkFixture({ failure });
    await runCheck();
    await waitFor(() => expect(rowAlert()).not.toBeNull());
    expect(
      within(rowAlert()!).getByText("DeliDev could not complete this request."),
    ).toBeTruthy();
    expect(screen.queryByText(/Could not connect to DeliDev on/)).toBeNull();
    expect(screen.getByText("DeliDev: Unavailable or blocked")).toBeTruthy();
    cleanup();
    document.body.replaceChildren();
  }
});

it("keeps the accepting state and actions after a successful check", async () => {
  checkFixture({});
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  expect(rowAlert()).toBeNull();
  expect(screen.getByRole("button", { name: "Connect" })).toBeTruthy();
});

// Stale-data class (issue 3687 review): TanStack Query keeps the previous data
// next to a newer error, so the latest result must control status and actions.
const actionNames = ["Connect", "Register Worker"];
const refusedFailure = () =>
  checkFailure("Original safe message.", "unavailable", "connection_refused");

it("lets a refused retry replace the cached accepting state and actions", async () => {
  const f = checkFixture({ failure: refusedFailure, failFrom: 2 });
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Connect" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Check" }));
  expect(await screen.findByText("DeliDev: Could not connect")).toBeTruthy();
  expect(screen.queryByText("DeliDev: Accepting requests")).toBeNull();
  for (const name of actionNames)
    expect(screen.queryByRole("button", { name })).toBeNull();
  expect(
    screen.getByText(`Could not connect to DeliDev on ${DEVICE}.`),
  ).toBeTruthy();
  expect(f.attempts()).toBe(2);
});

it("lets any later check failure replace the cached accepting state", async () => {
  checkFixture({
    failure: () =>
      checkFailure("Original safe message.", "unavailable", "timeout"),
    failFrom: 2,
  });
  await runCheck();
  await screen.findByText("DeliDev: Accepting requests");
  fireEvent.click(screen.getByRole("button", { name: "Check" }));
  expect(
    await screen.findByText("DeliDev: Unavailable or blocked"),
  ).toBeTruthy();
  expect(screen.queryByText("DeliDev: Accepting requests")).toBeNull();
  for (const name of actionNames)
    expect(screen.queryByRole("button", { name })).toBeNull();
  expect(rowAlert()).not.toBeNull();
});

it("ignores the cached inventory and its peers after a failed refresh", async () => {
  checkFixture({ discoveryFailFrom: 2 });
  expect(await screen.findByText("Tailscale connected")).toBeTruthy();
  expect(screen.getByText(DEVICE)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() =>
    expect(screen.queryByText("Tailscale connected")).toBeNull(),
  );
  expect(screen.getByText("Unavailable or blocked")).toBeTruthy();
  expect(screen.queryByText(DEVICE)).toBeNull();
  expect(screen.queryByRole("button", { name: "Check" })).toBeNull();
  expect(document.querySelector("[role='alert']")).not.toBeNull();
});

it("treats cached access as unknown when the latest access read fails", async () => {
  accessFixture({
    failure: () => unavailable("Unused", "Unused"),
    accessReadFailure: () => unavailable("Read failed", "Retry the read."),
    accessReadFailFrom: 2,
  });
  // The first read succeeded with access on; a failing refetch is triggered by
  // the polling interval while the Local view is current.
  expect(await screen.findByText("Tailscale access: On")).toBeTruthy();
  await waitFor(() => expect(accessAlert()).not.toBeNull(), { timeout: 4000 });
  expect(screen.queryByText("Tailscale access: On")).toBeNull();
  expect(
    screen.getByText("Tailscale access: Setup or cleanup required"),
  ).toBeTruthy();
});

// Invalidated-check class (issue 3687 review): a changed host or online state
// resets the check, so neither the cached failure nor the cached identity may
// show or be served again for the new peer state.
const movedHost = (discovery: number) =>
  discovery >= 2 ? { host: "moved.example" } : {};
const refresh = () =>
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

it("drops the cached cause diagnosis when discovery changes the peer host", async () => {
  checkFixture({
    failure: refusedFailure,
    failUntil: 1,
    peerAtDiscovery: movedHost,
  });
  await runCheck();
  expect(
    await screen.findByText(`Could not connect to DeliDev on ${DEVICE}.`),
  ).toBeTruthy();
  refresh();
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  expect(screen.queryByText(/Could not connect to DeliDev on/)).toBeNull();
  expect(screen.queryByText("DeliDev: Could not connect")).toBeNull();
  expect(rowAlert()).toBeNull();
});

it("drops a cached generic failure when discovery changes the peer online state", async () => {
  checkFixture({
    failure: () =>
      checkFailure("Original safe message.", "unavailable", "dns_failure"),
    peerAtDiscovery: (discovery) => ({ online: discovery < 2 }),
  });
  await runCheck();
  await waitFor(() => expect(rowAlert()).not.toBeNull());
  refresh();
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  expect(rowAlert()).toBeNull();
  expect(screen.queryByText("DeliDev: Unavailable or blocked")).toBeNull();
});

it("runs a fresh check after the host changed instead of serving the cached failure", async () => {
  const f = checkFixture({
    failure: refusedFailure,
    failUntil: 1,
    peerAtDiscovery: movedHost,
  });
  await runCheck();
  await screen.findByText(`Could not connect to DeliDev on ${DEVICE}.`);
  refresh();
  await screen.findByText("DeliDev: Not checked");
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  expect(f.attempts()).toBe(2);
  expect(rowAlert()).toBeNull();
});

it("drops the cached identity and its actions when discovery changes the peer host", async () => {
  const f = checkFixture({ peerAtDiscovery: movedHost });
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  refresh();
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  for (const name of actionNames)
    expect(screen.queryByRole("button", { name })).toBeNull();
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  expect(f.attempts()).toBe(2);
});

// Re-armed check class (issue 3687 review): returning to the originally checked
// host or online state must not match a retained key and run the query again.
it("stays Not checked when the host returns to the checked value", async () => {
  const f = checkFixture({
    peerAtDiscovery: (discovery) =>
      discovery === 2 ? { host: "moved.example" } : {},
  });
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  refresh();
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  refresh();
  await waitFor(() => expect(f.discoveries()).toBe(3));
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  for (const name of actionNames)
    expect(screen.queryByRole("button", { name })).toBeNull();
  expect(f.attempts()).toBe(1);
  runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  expect(f.attempts()).toBe(2);
});

it("stays Not checked when the peer returns online after being offline", async () => {
  const f = checkFixture({
    peerAtDiscovery: (discovery) => ({ online: discovery !== 2 }),
  });
  await runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  refresh();
  await waitFor(() => expect(f.discoveries()).toBe(2));
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  refresh();
  await waitFor(() => expect(f.discoveries()).toBe(3));
  expect(await screen.findByText("DeliDev: Not checked")).toBeTruthy();
  for (const name of actionNames)
    expect(screen.queryByRole("button", { name })).toBeNull();
  expect(rowAlert()).toBeNull();
  expect(f.attempts()).toBe(1);
  runCheck();
  expect(await screen.findByText("DeliDev: Accepting requests")).toBeTruthy();
  expect(f.attempts()).toBe(2);
});

// Unknown-state action class (issue 3687 review): while the access state is
// unknown, Enable and Disable are disabled so a failed read never sends a fresh
// request that could clean up a working ingress.
const accessButton = () =>
  screen.getByRole("button", { name: /^(Enable|Disable) access$/ });
it("disables the access action while the first access read is pending", async () => {
  let release!: () => void;
  const f = accessFixture({
    failure: () => unavailable("Unused", "Unused"),
    accessReadGate: new Promise<void>((resolve) => (release = resolve)),
  });
  const button = (await screen.findByRole("button", {
    name: "Enable access",
  })) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  fireEvent.click(button);
  expect(screen.queryByRole("dialog")).toBeNull();
  release();
  await waitFor(() => expect(button.disabled).toBe(false));
  expect(f.set).not.toHaveBeenCalled();
});

it("disables Enable and Disable when the latest access read fails after ready", async () => {
  const f = accessFixture({
    failure: () => unavailable("Unused", "Unused"),
    accessReadFailure: () => unavailable("Read failed", "Retry the read."),
    accessReadFailFrom: 2,
  });
  expect(await screen.findByText("Tailscale access: On")).toBeTruthy();
  expect((accessButton() as HTMLButtonElement).disabled).toBe(false);
  expect(accessButton().textContent).toBe("Disable access");
  await waitFor(() => expect(accessAlert()).not.toBeNull(), { timeout: 4000 });
  const button = accessButton() as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  fireEvent.click(button);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(f.set).not.toHaveBeenCalled();
});

it("disables the confirmation when the access read fails while it is open", async () => {
  const f = accessFixture({
    failure: () => unavailable("Unused", "Unused"),
    accessReadFailure: () => unavailable("Read failed", "Retry the read."),
    accessReadFailFrom: 2,
    accessBeforeFailure: { enabled: false, state: "off", server_id: "s" },
  });
  fireEvent.click(await screen.findByRole("button", { name: "Enable access" }));
  const dialog = await screen.findByRole("dialog");
  const confirm = within(dialog).getByRole("button", {
    name: "Enable access",
  }) as HTMLButtonElement;
  await waitFor(() => expect(accessAlert()).not.toBeNull(), { timeout: 4000 });
  expect(confirm.disabled).toBe(true);
  fireEvent.click(confirm);
  expect(f.set).not.toHaveBeenCalled();
});

it("restores the access action after a later successful read", async () => {
  accessFixture({
    failure: () => unavailable("Unused", "Unused"),
    accessReadFailure: () => unavailable("Read failed", "Retry the read."),
    accessReadFailFrom: 2,
    accessReadFailUntil: 2,
  });
  await screen.findByText("Tailscale access: On");
  await waitFor(() => expect(accessAlert()).not.toBeNull(), { timeout: 4000 });
  expect((accessButton() as HTMLButtonElement).disabled).toBe(true);
  await waitFor(
    () => expect((accessButton() as HTMLButtonElement).disabled).toBe(false),
    { timeout: 4000 },
  );
  expect(accessButton().textContent).toBe("Disable access");
});
