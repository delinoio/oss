// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  SubscriptionQuery,
  SubscriptionService,
  SystemCapability,
  SystemService,
  isEntityId,
  newRequestId,
  type RefreshAllSubscriptionQuotasRequest,
} from "@delinoio/delidev-api-client";
import {
  MutationIntents,
  subscriptionQuotaAllKey,
  useRetainedMutation,
} from "./mutation";
import {
  SubscriptionQuotaAutoRefresh,
  acknowledgeRefreshAllSubscriptionQuotas,
} from "./subscription-quota-refresh";

let focused = true,
  hidden = false;
beforeEach(() => {
  focused = true;
  hidden = false;
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() =>
    hidden ? "hidden" : "visible",
  );
  vi.spyOn(console, "info").mockImplementation(() => undefined);
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

type Respond = (
  request: RefreshAllSubscriptionQuotasRequest,
) => Promise<{ requestId: string; accounts: string[] }>;
const accepted: Respond = async (request) => ({
  requestId: request.requestId,
  accounts: [newRequestId()],
});
function fixture(
  respond: Respond = accepted,
  options: { capable?: boolean; stopping?: boolean } = {},
) {
  const requests: RefreshAllSubscriptionQuotasRequest[] = [];
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        stopping: options.stopping === true,
        capabilities:
          options.capable === false
            ? []
            : [SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2],
      }),
    });
    router.service(SubscriptionService, {
      refreshAllSubscriptionQuotas: async (request) => {
        requests.push(request);
        return respond(request);
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  // The probe stands in for Settings: it owns a nested registry that is
  // discarded on departure while the connection-owned retained key survives.
  function Probe() {
    const refresh = useRetainedMutation(
      subscriptionQuotaAllKey,
      SubscriptionQuery.refreshAllSubscriptionQuotas,
      undefined,
      acknowledgeRefreshAllSubscriptionQuotas,
    );
    return (
      <div>
        <p data-testid="state">
          {refresh.busy ? "busy" : refresh.uncertain ? "uncertain" : "idle"}
        </p>
        <p data-testid="error">{refresh.error ? "error" : "none"}</p>
        <button type="button" onClick={refresh.retry}>
          Retry original
        </button>
        <button
          type="button"
          onClick={() => void refresh.send({ requestId: newRequestId() })}
        >
          Manual refresh
        </button>
      </div>
    );
  }
  const view = ({ ready = true, probe = false } = {}) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SubscriptionQuotaAutoRefresh ready={ready} />
          {probe ? (
            <MutationIntents>
              <Probe />
            </MutationIntents>
          ) : null}
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return { requests, view };
}
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}
async function mount(ui: React.ReactElement) {
  const mounted = render(ui);
  // Let the status query settle without consuming any scheduled wait.
  await advance(0);
  return mounted;
}
function setForeground(next: { focus?: boolean; hide?: boolean }) {
  if (next.focus !== undefined) focused = next.focus;
  if (next.hide !== undefined) hidden = next.hide;
  fireEvent(document, new Event("visibilitychange"));
  fireEvent(window, new Event(focused ? "focus" : "blur"));
}

it("waits a full thirty seconds, then sends one fresh UUID-v7 batch per tick", async () => {
  const f = fixture();
  await mount(f.view());
  await advance(29999);
  expect(f.requests).toHaveLength(0);
  await advance(1);
  expect(f.requests).toHaveLength(1);
  await advance(30000);
  expect(f.requests).toHaveLength(2);
  const ids = f.requests.map((request) => request.requestId);
  expect(new Set(ids).size).toBe(2);
  expect(ids.every(isEntityId)).toBe(true);
});

it("keeps its thirty-second cadence through blur, hide and minimize for fifteen minutes", async () => {
  for (const mode of ["blur", "hide"] as const) {
    const f = fixture();
    const mounted = await mount(f.view());
    await advance(20000);
    act(() =>
      setForeground(mode === "blur" ? { focus: false } : { hide: true }),
    );
    // The first request still lands at the original thirty-second mark.
    await advance(9999);
    expect(f.requests).toHaveLength(0);
    await advance(1);
    expect(f.requests).toHaveLength(1);
    await advance(15 * 60 * 1000 - 30000);
    expect(f.requests).toHaveLength(30);
    expect(new Set(f.requests.map((request) => request.requestId)).size).toBe(
      30,
    );
    mounted.unmount();
  }
});

it("neither resets a running cadence nor sends an immediate duplicate when focus returns", async () => {
  const f = fixture();
  await mount(f.view());
  act(() => setForeground({ focus: false }));
  await advance(30000);
  expect(f.requests).toHaveLength(1);
  await advance(10000);
  act(() => setForeground({ focus: true }));
  await advance(0);
  expect(f.requests).toHaveLength(1);
  fireEvent(window, new Event("focus"));
  await advance(19999);
  expect(f.requests).toHaveLength(1);
  await advance(1);
  expect(f.requests).toHaveLength(2);
  await advance(30000);
  expect(f.requests).toHaveLength(3);
});

it("sends nothing while disconnected, unsupported or stopping, and restarts its wait on readiness", async () => {
  const unsupported = fixture(accepted, { capable: false });
  const first = await mount(unsupported.view());
  await advance(120000);
  expect(unsupported.requests).toHaveLength(0);
  first.unmount();
  const stopping = fixture(accepted, { stopping: true });
  const second = await mount(stopping.view());
  await advance(120000);
  expect(stopping.requests).toHaveLength(0);
  second.unmount();
  const f = fixture();
  const mounted = await mount(f.view({ ready: false }));
  await advance(120000);
  expect(f.requests).toHaveLength(0);
  mounted.rerender(f.view({ ready: true }));
  await advance(29999);
  expect(f.requests).toHaveLength(0);
  await advance(1);
  expect(f.requests).toHaveLength(1);
  mounted.rerender(f.view({ ready: false }));
  await advance(120000);
  expect(f.requests).toHaveLength(1);
});

it("stops on disposal", async () => {
  const f = fixture();
  const mounted = await mount(f.view());
  await advance(10000);
  mounted.unmount();
  await advance(120000);
  expect(f.requests).toHaveLength(0);
});

it("skips ticks while its shared request is busy and never replaces it", async () => {
  let release!: () => void;
  const f = fixture(
    (request) =>
      new Promise((resolve) => {
        release = () => resolve({ requestId: request.requestId, accounts: [] });
      }),
  );
  await mount(f.view({ probe: true }));
  await advance(30000);
  expect(f.requests).toHaveLength(1);
  expect(screen.getByTestId("state").textContent).toBe("busy");
  await advance(90000);
  fireEvent.click(screen.getByText("Manual refresh"));
  await advance(0);
  expect(f.requests).toHaveLength(1);
  await act(async () => release());
  expect(screen.getByTestId("state").textContent).toBe("idle");
  await advance(30000);
  expect(f.requests).toHaveLength(2);
});

it.each([
  [
    "unavailable",
    () => Promise.reject(new ConnectError("down", Code.Unavailable)),
  ],
  [
    "an invalid acknowledgement",
    (request: RefreshAllSubscriptionQuotasRequest) =>
      Promise.resolve({ requestId: newRequestId(), accounts: [] }),
  ],
])(
  "keeps an uncertain original request after %s, never retries it automatically, and retries only its bytes explicitly",
  async (_name, respond) => {
    const f = fixture(respond as Respond);
    const mounted = await mount(f.view({ probe: true }));
    await advance(30000);
    expect(f.requests).toHaveLength(1);
    expect(screen.getByTestId("state").textContent).toBe("uncertain");
    await advance(180000);
    expect(f.requests).toHaveLength(1);
    // Departing and re-entering Settings keeps the connection-owned original.
    mounted.rerender(f.view({ probe: false }));
    mounted.rerender(f.view({ probe: true }));
    expect(screen.getByTestId("state").textContent).toBe("uncertain");
    expect(screen.getByTestId("error").textContent).toBe("error");
    fireEvent.click(screen.getByText("Retry original"));
    await advance(0);
    expect(f.requests).toHaveLength(2);
    expect(f.requests[1].requestId).toBe(f.requests[0].requestId);
  },
);

it("permits a later fresh request after a definite rejection and keeps the error visible until then", async () => {
  let reject = true;
  const f = fixture(async (request) => {
    if (reject) throw new ConnectError("denied", Code.PermissionDenied);
    return accepted(request);
  });
  await mount(f.view({ probe: true }));
  await advance(30000);
  expect(f.requests).toHaveLength(1);
  expect(screen.getByTestId("state").textContent).toBe("idle");
  expect(screen.getByTestId("error").textContent).toBe("error");
  reject = false;
  await advance(30000);
  expect(f.requests).toHaveLength(2);
  expect(f.requests[1].requestId).not.toBe(f.requests[0].requestId);
  expect(screen.getByTestId("error").textContent).toBe("none");
});

it("validates the exact request and a bounded unique inventory", () => {
  const request = {
    requestId: newRequestId(),
  } as RefreshAllSubscriptionQuotasRequest;
  const a = newRequestId();
  const response = (requestId: string, accounts: string[]) =>
    ({ requestId, accounts }) as never;
  expect(
    acknowledgeRefreshAllSubscriptionQuotas(
      response(request.requestId, [a]),
      request,
    ),
  ).toBe(true);
  expect(
    acknowledgeRefreshAllSubscriptionQuotas(
      response(newRequestId(), []),
      request,
    ),
  ).toBe(false);
  expect(
    acknowledgeRefreshAllSubscriptionQuotas(
      response(request.requestId, [a, a]),
      request,
    ),
  ).toBe(false);
  expect(
    acknowledgeRefreshAllSubscriptionQuotas(
      response(request.requestId, ["not-an-id"]),
      request,
    ),
  ).toBe(false);
  expect(
    acknowledgeRefreshAllSubscriptionQuotas(
      response(
        request.requestId,
        Array.from({ length: 10001 }, () => newRequestId()),
      ),
      request,
    ),
  ).toBe(false);
});
