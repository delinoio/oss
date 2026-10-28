// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, rs } from "@rstest/core";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type ConnectRouter,
} from "@connectrpc/connect";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  ErrorDetailSchema,
  HealthState,
  OperatorService,
  Role,
  type GetInstanceUsageRequest,
} from "@tairouter/client";
import { create } from "@bufbuild/protobuf";
import { ApiProvider } from "../api";
import { AdminAuthority } from "./common";
import { Health } from "./operator";

afterEach(() => {
  cleanup();
  rs.useRealTimers();
});

function mount(routes: (router: ConnectRouter) => void) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    routes(router);
  });
  const view = render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <AdminAuthority.Provider
        value={{
          organizationId: "",
          role: Role.UNSPECIFIED,
          onDenied: () => {},
        }}
      >
        <Health />
      </AdminAuthority.Provider>
    </ApiProvider>,
  );
  return { client, view };
}

const primary = {
  requestCount: 1248n,
  failedRequestCount: 18n,
  knownUsage: { inputTokens: 284600n, outputTokens: 96200n },
  knownCosts: [{ currency: "USD", decimalAmount: "12.3400" }],
  missingCostCount: 3n,
  missingUsageCount: 2n,
  missingPriceCount: 1n,
};

function operator(
  health: object | undefined,
  summary: object | null = primary,
) {
  return (router: ConnectRouter) =>
    router.service(OperatorService, {
      getInstanceHealth: () => ({ health }),
      getInstanceUsage: () => ({ summary: summary ?? undefined }),
    });
}

const badgeFor = (name: string) =>
  within(screen.getByText(name).closest("div") as HTMLElement).getByText(
    /^(Healthy|Degraded|Unavailable|Unknown)$/,
  );

describe("instance health body (scenario 1)", () => {
  it("renders the availability and aggregate hierarchy without tenant controls", async () => {
    mount(
      operator({
        service: HealthState.HEALTHY,
        database: HealthState.HEALTHY,
        usageRecording: HealthState.UNSPECIFIED,
      }),
    );
    expect(
      screen.getByRole("heading", { level: 1, name: "Instance health" }),
    ).toBeTruthy();
    expect(
      screen.getByText("Whole-instance availability and request aggregates."),
    ).toBeTruthy();
    expect(
      screen.getAllByText("Whole-instance health and aggregates only."),
    ).toHaveLength(1);
    expect(
      screen.getByRole("heading", { level: 2, name: "Availability" }),
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", {
        level: 2,
        name: "Instance usage aggregates",
      }),
    ).toBeTruthy();
    expect(screen.getByText("Health refreshes every 30 seconds")).toBeTruthy();
    expect(await screen.findByText("1,248")).toBeTruthy();
    const terms = [
      "Requests",
      "Failed requests",
      "Confirmed input tokens",
      "Confirmed output tokens",
      "Known estimated cost",
    ];
    const rendered = screen
      .getAllByRole("term")
      .map((term) => term.textContent)
      .filter((text) => terms.includes(text ?? ""));
    expect(rendered).toEqual(terms);
    for (const value of ["18", "284,600", "96,200", "USD 12.3400"])
      expect(screen.getByText(value)).toBeTruthy();
    expect(screen.getByText("3 requests with unknown cost")).toBeTruthy();
    expect(screen.getByText("2 missing usage")).toBeTruthy();
    expect(screen.getByText("1 missing price")).toBeTruthy();
    expect(screen.getByText("Request totals may be incomplete")).toBeTruthy();
    expect(
      screen.getByText(
        "Unknown recording health does not confirm complete totals.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Recording failures can leave request totals incomplete.",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Estimates, not billing. Currency is shown with each known cost. Price changes apply to new requests only.",
      ),
    ).toBeTruthy();
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(screen.queryAllByRole("combobox")).toHaveLength(0);
    expect(screen.queryByText(/all totals complete/i)).toBeNull();
  });
});

describe("health states (scenario 2)", () => {
  it.each([
    [HealthState.UNSPECIFIED, "Unknown", "neutral"],
    [HealthState.HEALTHY, "Healthy", "success"],
    [HealthState.DEGRADED, "Degraded", "warning"],
    [HealthState.UNAVAILABLE, "Unavailable", "danger"],
    [99 as HealthState, "Unknown", "neutral"],
  ])("renders %s as %s", async (state, text, tone) => {
    mount(operator({ service: state, database: state, usageRecording: state }));
    await screen.findByText("1,248");
    for (const name of ["Service", "Database", "Usage recording"]) {
      const badge = badgeFor(name);
      expect(badge.textContent).toBe(text);
      expect(badge.className).toContain(tone);
      expect(badge.classList.contains("success")).toBe(text === "Healthy");
    }
  });

  it("renders every status as Unknown when the health object is missing", async () => {
    mount(operator(undefined));
    await screen.findByText("1,248");
    for (const name of ["Service", "Database", "Usage recording"])
      expect(badgeFor(name).textContent).toBe("Unknown");
    expect(screen.getByText("Request totals may be incomplete")).toBeTruthy();
  });

  it("keeps the caution but never certifies totals when recording is Healthy", async () => {
    mount(operator({ usageRecording: HealthState.HEALTHY }));
    await screen.findByText("1,248");
    expect(screen.queryByText("Request totals may be incomplete")).toBeNull();
    expect(
      screen.getByText(
        "Recorded metadata may be incomplete if usage recording failed.",
      ),
    ).toBeTruthy();
    expect(screen.queryByText(/complete totals/i)).toBeNull();
    expect(screen.queryByText(/all totals/i)).toBeNull();
  });

  it.each([
    [HealthState.DEGRADED, false],
    [HealthState.UNAVAILABLE, false],
    [HealthState.UNSPECIFIED, true],
  ])("shows the callout for recording %s", async (state, unknown) => {
    mount(operator({ usageRecording: state }));
    expect(
      await screen.findByText("Request totals may be incomplete"),
    ).toBeTruthy();
    expect(
      !!screen.queryByText(
        "Unknown recording health does not confirm complete totals.",
      ),
    ).toBe(unknown);
  });
});

describe("cleanup evidence (scenario 3)", () => {
  const cleanup_ = (name: string) =>
    (screen.getByText(name).closest("div") as HTMLElement).querySelector("dd")
      ?.textContent;
  it.each([0, 3])(
    "shows Unknown for failures=%s without a cleanup timestamp",
    async (failures) => {
      mount(operator({ consecutiveCleanupFailures: failures }));
      await screen.findByText("1,248");
      expect(cleanup_("Last successful cleanup")).toBe("Unknown");
      expect(cleanup_("Consecutive cleanup failures")).toBe("Unknown");
    },
  );
  it.each([0, 3])(
    "shows the exact count %s with a cleanup timestamp",
    async (failures) => {
      const at = new Date("2026-10-27T01:02:03Z");
      mount(
        operator({
          lastCleanupSuccessAt: {
            seconds: BigInt(at.getTime() / 1000),
            nanos: 0,
          },
          consecutiveCleanupFailures: failures,
          service: HealthState.UNAVAILABLE,
        }),
      );
      await screen.findByText("1,248");
      expect(cleanup_("Consecutive cleanup failures")).toBe(String(failures));
      expect(cleanup_("Last successful cleanup")).toContain(
        `(${Intl.DateTimeFormat().resolvedOptions().timeZone})`,
      );
      expect(badgeFor("Service").textContent).toBe("Unavailable");
    },
  );
});

describe("polling and manual refresh (scenario 4)", () => {
  it("polls health only every 30 seconds and refreshes health only on demand", async () => {
    rs.useFakeTimers();
    const health = rs.fn<(request: object) => void>();
    const usage = rs.fn<(request: GetInstanceUsageRequest) => void>();
    let release: (() => void) | undefined;
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: async (request) => {
          health(request);
          if (release === undefined && health.mock.calls.length === 3)
            await new Promise<void>((resolve) => (release = resolve));
          return { health: { usageRecording: HealthState.HEALTHY } };
        },
        getInstanceUsage: (request) => {
          usage(request);
          return { summary: primary };
        },
      }),
    );
    await act(() => rs.advanceTimersByTimeAsync(0));
    expect(health).toHaveBeenCalledTimes(1);
    expect(Object.keys(health.mock.calls[0][0])).toEqual(["$typeName"]);
    expect(usage).toHaveBeenCalledTimes(1);
    await act(() => rs.advanceTimersByTimeAsync(29000));
    expect(health).toHaveBeenCalledTimes(1);
    await act(() => rs.advanceTimersByTimeAsync(1000));
    expect(health).toHaveBeenCalledTimes(2);
    expect(usage).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("Health refreshed.")).toBeNull();

    const button = screen.getByRole("button", { name: "Refresh health" });
    button.focus();
    await act(async () => {
      fireEvent.click(button);
    });
    await act(() => rs.advanceTimersByTimeAsync(0));
    expect(health).toHaveBeenCalledTimes(3);
    const busy = screen.getByRole("button", { name: "Refreshing…" });
    expect(busy.getAttribute("aria-disabled")).toBe("true");
    expect(document.activeElement).toBe(busy);
    await act(async () => {
      fireEvent.click(busy);
      fireEvent.click(busy);
    });
    expect(health).toHaveBeenCalledTimes(3);
    await act(async () => release?.());
    await act(() => rs.advanceTimersByTimeAsync(0));
    const restored = screen.getByRole("button", { name: "Refresh health" });
    expect(document.activeElement).toBe(restored);
    expect(restored.getAttribute("aria-disabled")).toBe("false");
    expect(screen.getByText("Health refreshed.")).toBeTruthy();
    expect(usage).toHaveBeenCalledTimes(1);

    cleanup();
    await act(() => rs.advanceTimersByTimeAsync(120000));
    expect(health).toHaveBeenCalledTimes(3);
    expect(usage).toHaveBeenCalledTimes(1);
  });
});

describe("usage dates (scenario 5)", () => {
  it("uses the existing initial range, full UTC days and blocks invalid ranges", async () => {
    rs.useFakeTimers({ toFake: ["Date"] });
    rs.setSystemTime(new Date("2026-10-27T12:00:00Z"));
    const usage = rs.fn<(request: GetInstanceUsageRequest) => void>();
    const health = rs.fn();
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: () => {
          health();
          return { health: {} };
        },
        getInstanceUsage: (request) => {
          usage(request);
          const day = request.timeRange?.start
            ? timestampDate(request.timeRange.start).toISOString().slice(0, 10)
            : "";
          return {
            summary: {
              ...primary,
              requestCount: day === "2026-10-21" ? 7n : 5n,
            },
          };
        },
      }),
    );
    const from = screen.getByLabelText("From (UTC)") as HTMLInputElement;
    const through = screen.getByLabelText("Through (UTC)") as HTMLInputElement;
    expect(from.value).toBe("2026-10-20");
    expect(through.value).toBe("2026-10-27");
    expect(
      screen.getByText("Dates include the full selected days in UTC."),
    ).toBeTruthy();
    await screen.findByText("5");
    expect(usage).toHaveBeenCalledTimes(1);

    fireEvent.change(from, { target: { value: "2026-10-21" } });
    fireEvent.change(through, { target: { value: "2026-10-21" } });
    await screen.findByText("7");
    const last = usage.mock.lastCall?.[0];
    expect(timestampDate(last!.timeRange!.start!).toISOString()).toBe(
      "2026-10-21T00:00:00.000Z",
    );
    expect(timestampDate(last!.timeRange!.end!).toISOString()).toBe(
      "2026-10-21T23:59:59.999Z",
    );

    const before = usage.mock.calls.length;
    fireEvent.change(from, { target: { value: "" } });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("Choose a valid start and end date.");
    for (const input of [from, through]) {
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(input.getAttribute("aria-describedby")).toContain(alert.id);
    }
    expect(screen.queryByText("7")).toBeNull();
    fireEvent.change(from, { target: { value: "2026-10-22" } });
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(usage.mock.calls.length).toBe(before);
    expect(screen.queryByText("Requests")).toBeNull();
    // Health stays usable while the range is invalid.
    expect(screen.getByRole("button", { name: "Refresh health" })).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Refresh health" }));
    });
    await waitFor(() => expect(health).toHaveBeenCalledTimes(2));
    expect(usage.mock.calls.length).toBe(before);

    fireEvent.change(from, { target: { value: "2026-10-21" } });
    expect(await screen.findByText("7")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("summary values (scenario 6)", () => {
  it("shows an unavailable state when the summary is absent", async () => {
    mount(operator({}, null));
    expect(
      await screen.findByText("Usage summary is unavailable."),
    ).toBeTruthy();
    expect(
      screen.queryByText("No recorded requests in this period."),
    ).toBeNull();
  });

  it("shows exact zero fields for a zero-request summary", async () => {
    mount(
      operator(
        {},
        {
          requestCount: 0n,
          failedRequestCount: 0n,
          knownUsage: { inputTokens: 0n, outputTokens: 0n },
          knownCosts: [{ currency: "USD", decimalAmount: "0" }],
          missingCostCount: 0n,
          missingUsageCount: 0n,
          missingPriceCount: 0n,
        },
      ),
    );
    expect(
      await screen.findByText("No recorded requests in this period."),
    ).toBeTruthy();
    const values = screen
      .getAllByRole("definition")
      .map((node) => node.textContent);
    expect(values).toContain("USD 0");
    expect(values.filter((value) => value === "0")).toHaveLength(4);
    expect(screen.getByText("0 requests with unknown cost")).toBeTruthy();
    expect(screen.getByText("0 missing usage")).toBeTruthy();
    expect(screen.getByText("0 missing price")).toBeTruthy();
  });

  const valueOf = (term: string) =>
    (screen.getByText(term).closest("div") as HTMLElement).querySelector("dd")
      ?.textContent;

  it("distinguishes absent usage and cost from present zero", async () => {
    mount(
      operator(
        {},
        {
          requestCount: 3n,
          failedRequestCount: 3n,
          knownUsage: { inputTokens: 0n },
          knownCosts: [],
          missingCostCount: 3n,
          missingUsageCount: 3n,
          missingPriceCount: 0n,
        },
      ),
    );
    await screen.findByText("3 missing usage");
    expect(valueOf("Confirmed input tokens")).toBe("0");
    expect(valueOf("Confirmed output tokens")).toBe("Unknown");
    expect(valueOf("Known estimated cost")).toBe("Unknown");
    expect(valueOf("Requests")).toBe("3");
  });

  it("treats a missing knownUsage as Unknown for both token metrics", async () => {
    mount(operator({}, { ...primary, knownUsage: undefined }));
    await screen.findByText("1,248");
    expect(valueOf("Confirmed input tokens")).toBe("Unknown");
    expect(valueOf("Confirmed output tokens")).toBe("Unknown");
  });

  it("keeps every currency separate with exact decimals and large integers", async () => {
    mount(
      operator(
        {},
        {
          ...primary,
          requestCount: 9007199254740993n,
          failedRequestCount: 9007199254740992n,
          knownCosts: [
            { currency: "USD", decimalAmount: "0" },
            { currency: "EUR", decimalAmount: "1.2500" },
            { currency: "JPY", decimalAmount: "123456789012345678.123456789" },
          ],
        },
      ),
    );
    expect(await screen.findByText("9,007,199,254,740,993")).toBeTruthy();
    expect(screen.getByText("9,007,199,254,740,992")).toBeTruthy();
    const costs = within(
      screen.getByText("Known estimated cost").closest("div") as HTMLElement,
    )
      .getAllByRole("listitem")
      .map((item) => item.textContent);
    expect(costs).toEqual([
      "USD 0",
      "EUR 1.2500",
      "JPY 123,456,789,012,345,678.123456789",
    ]);
  });

  it("keeps the three missing counts distinct and unsummed", async () => {
    mount(
      operator(
        {},
        {
          ...primary,
          missingCostCount: 5n,
          missingUsageCount: 0n,
          missingPriceCount: 7n,
        },
      ),
    );
    expect(
      await screen.findByText("5 requests with unknown cost"),
    ).toBeTruthy();
    expect(screen.getByText("0 missing usage")).toBeTruthy();
    expect(screen.getByText("7 missing price")).toBeTruthy();
    expect(screen.queryByText(/12 /)).toBeNull();
  });
});

describe("independent boundaries (scenario 7)", () => {
  const unavailable = () => {
    throw new ConnectError(
      "raw backend text must not appear",
      Code.Unavailable,
      undefined,
      [
        {
          desc: ErrorDetailSchema,
          value: create(ErrorDetailSchema, { requestId: "req-health-1" }),
        },
      ],
    );
  };

  it("shows a health skeleton while usage is usable, then fails health independently", async () => {
    let releaseHealth: () => void = () => {};
    const usage = rs.fn();
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: async () => {
          await new Promise<void>((resolve) => (releaseHealth = resolve));
          return unavailable();
        },
        getInstanceUsage: () => {
          usage();
          return { summary: primary };
        },
      }),
    );
    expect(screen.getByText("Loading health…")).toBeTruthy();
    expect(screen.queryByText("Healthy")).toBeNull();
    expect(screen.queryByText("Degraded")).toBeNull();
    expect(await screen.findByText("1,248")).toBeTruthy();
    expect(screen.getByLabelText("From (UTC)")).toBeTruthy();
    releaseHealth();
    expect(await screen.findByText(/The service is unavailable/)).toBeTruthy();
    expect(screen.getByText("Request ID: req-health-1")).toBeTruthy();
    expect(screen.queryByText(/raw backend text/)).toBeNull();
    expect(screen.queryByText("Service")).toBeNull();
    expect(screen.getByText("Request totals may be incomplete")).toBeTruthy();
    expect(screen.getByText("1,248")).toBeTruthy();
    expect(usage).toHaveBeenCalledTimes(1);
  });

  it("fails usage independently and retries only usage", async () => {
    const health = rs.fn();
    let failing = true;
    const usage = rs.fn();
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: () => {
          health();
          return { health: { usageRecording: HealthState.HEALTHY } };
        },
        getInstanceUsage: () => {
          usage();
          if (failing) return unavailable();
          return { summary: primary };
        },
      }),
    );
    expect(await screen.findByText(/The service is unavailable/)).toBeTruthy();
    expect(badgeFor("Service").textContent).toBe("Unknown");
    expect(screen.getByLabelText("From (UTC)")).toBeTruthy();
    expect(screen.queryByText("Requests")).toBeNull();
    failing = false;
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("1,248")).toBeTruthy();
    expect(usage).toHaveBeenCalledTimes(2);
    expect(health).toHaveBeenCalledTimes(1);
  });

  it("shows the usage skeleton without placeholder totals", async () => {
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: () => ({ health: {} }),
        getInstanceUsage: () => new Promise(() => {}),
      }),
    );
    expect(await screen.findByText("Loading usage aggregates…")).toBeTruthy();
    expect(screen.queryByText("Requests")).toBeNull();
    expect(screen.queryByText("0")).toBeNull();
  });

  it("does not show old values as current after a failed background refetch", async () => {
    let failing = false;
    const health = rs.fn();
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: () => {
          health();
          if (failing) return unavailable();
          return { health: { service: HealthState.HEALTHY } };
        },
        getInstanceUsage: () => ({ summary: primary }),
      }),
    );
    await waitFor(() =>
      expect(badgeFor("Service").textContent).toBe("Healthy"),
    );
    failing = true;
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Refresh health" }));
    expect(await screen.findByText(/The service is unavailable/)).toBeTruthy();
    expect(screen.queryByText("Healthy")).toBeNull();
    expect(screen.queryByText("Refreshing…")).toBeNull();
    expect(screen.queryByText("Health refreshed.")).toBeNull();
    failing = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(badgeFor("Service").textContent).toBe("Healthy"),
    );
  });

  it("ignores a late response for a previous date range", async () => {
    const pending = new Map<string, () => void>();
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: () => ({ health: {} }),
        getInstanceUsage: async (request) => {
          const start = timestampDate(request.timeRange!.start!)
            .toISOString()
            .slice(0, 10);
          const end = timestampDate(request.timeRange!.end!)
            .toISOString()
            .slice(0, 10);
          const key = `${start}:${end}`;
          await new Promise<void>((resolve) => pending.set(key, resolve));
          return {
            summary: {
              ...primary,
              requestCount: end === start ? 222n : 111n,
            },
          };
        },
      }),
    );
    const from = screen.getByLabelText("From (UTC)") as HTMLInputElement;
    const through = screen.getByLabelText("Through (UTC)") as HTMLInputElement;
    await waitFor(() => expect(pending.size).toBe(1));
    const [firstKey] = [...pending.keys()];
    fireEvent.change(from, { target: { value: through.value } });
    await waitFor(() => expect(pending.size).toBe(2));
    const secondKey = [...pending.keys()].find((key) => key !== firstKey)!;
    // The current range resolves first; the stale range resolves afterwards.
    await act(async () => pending.get(secondKey)!());
    expect(await screen.findByText("222")).toBeTruthy();
    await act(async () => pending.get(firstKey)!());
    expect(screen.getByText("222")).toBeTruthy();
    expect(screen.queryByText("111")).toBeNull();
  });
});

describe("error guidance (scenario 8)", () => {
  it.each([
    [Code.Unauthenticated, /Your session expired/],
    [Code.PermissionDenied, /Your permissions changed/],
    [Code.Unavailable, /The service is unavailable/],
  ])(
    "uses the existing guidance for code %s without raw content",
    async (code, text) => {
      mount((router) =>
        router.service(OperatorService, {
          getInstanceHealth: () => {
            throw new ConnectError("secret-tenant-content", code);
          },
          getInstanceUsage: () => {
            throw new ConnectError("secret-tenant-content", code);
          },
        }),
      );
      expect((await screen.findAllByText(text)).length).toBe(2);
      expect(screen.queryByText(/secret-tenant-content/)).toBeNull();
      expect(screen.queryByText("Requests")).toBeNull();
      expect(screen.queryByText("Healthy")).toBeNull();
    },
  );

  it("sends health without arguments and usage with only a time range", async () => {
    const health = rs.fn<(request: object) => void>();
    const usage = rs.fn<(request: GetInstanceUsageRequest) => void>();
    mount((router) =>
      router.service(OperatorService, {
        getInstanceHealth: (request) => {
          health(request);
          return { health: {} };
        },
        getInstanceUsage: (request) => {
          usage(request);
          return { summary: primary };
        },
      }),
    );
    await screen.findByText("1,248");
    expect(Object.keys(health.mock.calls[0][0])).toEqual(["$typeName"]);
    expect(Object.keys(usage.mock.calls[0][0]).sort()).toEqual([
      "$typeName",
      "timeRange",
    ]);
  });
});
