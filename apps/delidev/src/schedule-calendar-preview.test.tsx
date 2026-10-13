// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { TimestampSchema, timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type Transport,
} from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ScheduleService,
  ScheduleCalendarConstraintsSchema,
  ScheduleCalendarDayMatch,
} from "@delinoio/delidev-api-client";
import {
  CalendarPreviewState,
  calendarRecurrence,
  calendarRelative,
  ScheduleCalendarSummary,
  useScheduleCalendarPreview,
} from "./schedule-calendar-preview";
import { i18n } from "./localization";

const all = (first: number, last: number) =>
  Array.from({ length: last - first + 1 }, (_, index) => first + index);
const constraints = create(ScheduleCalendarConstraintsSchema, {
  minutes: [0],
  hours: [9],
  daysOfMonth: all(1, 31),
  months: all(1, 12),
  weekdays: [1, 2, 3, 4, 5],
  dayMatch: ScheduleCalendarDayMatch.AND,
});
const response = () => ({
  calculatedAt: timestampFromDate(new Date("2026-10-10T09:00:00Z")),
  nextRunAt: timestampFromDate(new Date("2026-10-12T09:00:00Z")),
  constraints,
});
afterEach(async () => {
  await i18n.changeLanguage("en");
  vi.useRealTimers();
});
it("localizes normalized weekdays and preserves OR rather than inventing AND", async () => {
  expect(calendarRecurrence(constraints)).toBe(
    "Every Monday to Friday at 9:00 AM",
  );
  expect(
    calendarRelative(
      Date.parse("2026-10-12T09:00:00Z"),
      Date.parse("2026-10-10T09:00:00Z"),
    ),
  ).toBe("In 2 days");
  const or = create(ScheduleCalendarConstraintsSchema, {
    ...constraints,
    daysOfMonth: [1],
    weekdays: [1],
    dayMatch: ScheduleCalendarDayMatch.OR,
  });
  expect(calendarRecurrence(or)).toContain(
    "day of month 1 or day of week Monday",
  );
  await i18n.changeLanguage("ko");
  expect(calendarRecurrence(constraints)).toContain("월요일부터 금요일까지");
  expect(calendarRecurrence(or)).toContain("또는");
});
it("separates paused activation from a provisional enabled first run and collapses raw cron", () => {
  const preview = {
    state: CalendarPreviewState.Ready,
    constraints,
    next: Date.parse("2026-10-12T09:00:00Z"),
    now: Date.parse("2026-10-10T09:00:00Z"),
  };
  const props = {
    preview,
    cron: "0 9 * * 1-5",
    timezone: "UTC",
    fallback: "Weekdays",
  };
  const view = render(<ScheduleCalendarSummary {...props} enabled />);
  expect(screen.getByText("After you save this schedule")).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain(
    "Monday, October 12, 2026",
  );
  expect(screen.getByText("Cron details").closest("details")!.open).toBe(false);
  view.rerender(<ScheduleCalendarSummary {...props} enabled={false} />);
  expect(screen.getByText("After you enable this schedule")).toBeTruthy();
  expect(screen.getByRole("status").textContent).toBe(
    "No automatic run is scheduled.",
  );
});
function Harness({ cron, active = true }: { cron: string; active?: boolean }) {
  const preview = useScheduleCalendarPreview(cron, "UTC", active, true);
  return (
    <>
      <output>{preview.state}</output>
      <span>{preview.next ?? "no-date"}</span>
    </>
  );
}
function fixture(transport: Transport, cron = "0 9 * * 1-5") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const node = (
    currentTransport: Transport,
    currentCron: string,
    active = true,
  ) => (
    <TransportProvider transport={currentTransport}>
      <QueryClientProvider client={client}>
        <Harness cron={currentCron} active={active} />
      </QueryClientProvider>
    </TransportProvider>
  );
  const view = render(node(transport, cron));
  return {
    ...view,
    update: (currentTransport = transport, currentCron = cron, active = true) =>
      view.rerender(node(currentTransport, currentCron, active)),
  };
}
it("debounces, hides obsolete dates and rejects changed-input and replaced-connection responses", async () => {
  let finish: ((value: ReturnType<typeof response>) => void) | undefined;
  const preview = vi.fn(
    () =>
      new Promise<ReturnType<typeof response>>((resolve) => {
        finish = resolve;
      }),
  );
  const transport = createRouterTransport((router) =>
    router.service(ScheduleService, { previewScheduleCalendar: preview }),
  );
  const view = fixture(transport);
  expect(preview).not.toHaveBeenCalled();
  await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  const old = finish!;
  view.update(transport, "0 10 * * *");
  expect(screen.getByText("no-date")).toBeTruthy();
  await act(async () => old(response()));
  expect(screen.getByText("no-date")).toBeTruthy();
  await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
  const changed = finish!;
  const replacement = createRouterTransport((router) =>
    router.service(ScheduleService, {
      previewScheduleCalendar: () => {
        throw new ConnectError("Unavailable", Code.Unavailable);
      },
    }),
  );
  view.update(replacement, "0 10 * * *");
  await act(async () => changed(response()));
  await screen.findByText(CalendarPreviewState.Unavailable);
  expect(screen.getByText("no-date")).toBeTruthy();
});
it("supports Unimplemented without a date and refreshes on returning to the active step", async () => {
  const preview = vi.fn(async () => {
    throw new ConnectError("Old server", Code.Unimplemented);
  });
  const transport = createRouterTransport((router) =>
    router.service(ScheduleService, { previewScheduleCalendar: preview }),
  );
  const view = fixture(transport);
  await screen.findByText(CalendarPreviewState.Unsupported);
  expect(screen.getByText("no-date")).toBeTruthy();
  view.update(transport, "0 9 * * 1-5", false);
  view.update();
  await waitFor(() => expect(preview).toHaveBeenCalledTimes(2));
});
it("uses the server boundary across browser-clock skew and refreshes at the due boundary", async () => {
  vi.useFakeTimers({
    toFake: [
      "Date",
      "setTimeout",
      "clearTimeout",
      "setInterval",
      "clearInterval",
      "performance",
    ],
  });
  const preview = vi.fn(async () => ({
    ...response(),
    nextRunAt: timestampFromDate(new Date("2026-10-10T09:00:01Z")),
  }));
  const transport = createRouterTransport((router) =>
    router.service(ScheduleService, { previewScheduleCalendar: preview }),
  );
  fixture(transport);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(300);
  });
  expect(preview).toHaveBeenCalledTimes(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1);
  });
  expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1100);
  });
  expect(preview).toHaveBeenCalledTimes(2);
});

const fakeClock = (skewMs = 0) => {
  vi.useFakeTimers({
    toFake: [
      "Date",
      "setTimeout",
      "clearTimeout",
      "setInterval",
      "clearInterval",
      "performance",
    ],
  });
  vi.setSystemTime(new Date(Date.parse("2026-10-10T09:00:00Z") + skewMs));
};
const advance = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
const at = (offsetMs: number) =>
  timestampFromDate(new Date(Date.parse("2026-10-10T09:00:00Z") + offsetMs));
interface Reply {
  delayMs?: number;
  body: () => unknown;
}
// Serves one scripted reply per call; the last reply repeats.
function scripted(replies: Reply[]) {
  const handler = vi.fn(async () => {
    const reply =
      replies[Math.min(handler.mock.calls.length - 1, replies.length - 1)];
    if (reply.delayMs)
      await new Promise((resolve) => setTimeout(resolve, reply.delayMs));
    return reply.body() as ReturnType<typeof response>;
  });
  return {
    handler,
    transport: createRouterTransport((router) =>
      router.service(ScheduleService, { previewScheduleCalendar: handler }),
    ),
  };
}
const firstRunText = () =>
  document.querySelector(".schedule-calendar-summary [role=status]")
    ?.textContent;
function SummaryHarness() {
  const preview = useScheduleCalendarPreview("0 9 * * 1-5", "UTC", true, true);
  return (
    <>
      <output>{preview.state}</output>
      <span>{preview.next ?? "no-date"}</span>
      <ScheduleCalendarSummary
        preview={preview}
        cron="0 9 * * 1-5"
        timezone="UTC"
        enabled
        fallback="FALLBACK-RECURRENCE"
      />
    </>
  );
}
function mountSummary(transport: Transport) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SummaryHarness />
      </QueryClientProvider>
    </TransportProvider>,
  );
}
it.each([0, 3_600_000, -86_400_000])(
  "counts response latency toward the boundary refresh with a %i ms skewed browser clock",
  async (skew) => {
    fakeClock(skew);
    // Debounce 300 ms, request starts at 300 ms, response arrives at 1300 ms.
    const { handler, transport } = scripted([
      {
        delayMs: 1000,
        body: () => ({ ...response(), nextRunAt: at(10_000) }),
      },
    ]);
    mountSummary(transport);
    await advance(300);
    expect(handler).toHaveBeenCalledTimes(1);
    await advance(1001);
    expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
    // Boundary is 10000 ms after calculated_at; 1000 ms of it elapsed in flight.
    await advance(8_000);
    expect(handler).toHaveBeenCalledTimes(1);
    expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
    await advance(1_100);
    expect(handler).toHaveBeenCalledTimes(2);
  },
);
it("does not show a boundary that elapsed in flight as Ready and refetches immediately", async () => {
  fakeClock();
  const { handler, transport } = scripted([
    { delayMs: 1000, body: () => ({ ...response(), nextRunAt: at(500) }) },
    {
      delayMs: 1000,
      body: () => ({ ...response(), nextRunAt: at(3_600_000) }),
    },
  ]);
  mountSummary(transport);
  await advance(300);
  expect(handler).toHaveBeenCalledTimes(1);
  await advance(1001);
  // The first response is already past its boundary on arrival.
  expect(screen.queryByText(CalendarPreviewState.Ready)).toBeNull();
  expect(screen.getByText("no-date")).toBeTruthy();
  await advance(1);
  expect(handler).toHaveBeenCalledTimes(2);
  expect(screen.getByText(CalendarPreviewState.Calculating)).toBeTruthy();
  expect(firstRunText()).toBe("Calculating the first run…");
  await advance(1001);
  expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
});
it("anchors minute refreshes to the refresh request start", async () => {
  fakeClock();
  const { handler, transport } = scripted([
    { body: () => ({ ...response(), nextRunAt: at(130_000) }) },
    {
      delayMs: 2_000,
      body: () => ({
        ...response(),
        calculatedAt: at(60_000),
        nextRunAt: at(70_000),
      }),
    },
  ]);
  mountSummary(transport);
  await advance(301);
  await advance(50);
  expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
  // The minute refresh starts at 60300 ms and answers 2000 ms later.
  await advance(60_000);
  expect(handler).toHaveBeenCalledTimes(2);
  await advance(2_001);
  expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
  // Boundary is 10000 ms after calculated_at, 2000 ms of which were in flight.
  await advance(7_900);
  expect(handler).toHaveBeenCalledTimes(2);
  await advance(200);
  expect(handler).toHaveBeenCalledTimes(3);
});
const bare = (patch: Record<string, unknown>) => () => ({
  ...response(),
  ...patch,
});
it.each([
  ["missing calculated_at", bare({ calculatedAt: undefined })],
  ["missing next_run_at", bare({ nextRunAt: undefined })],
  ["missing constraints", bare({ constraints: undefined })],
  ["next equal to calculated", bare({ nextRunAt: at(0) })],
  ["next before calculated", bare({ nextRunAt: at(-60_000) })],
  [
    "out-of-range next seconds",
    bare({
      nextRunAt: create(TimestampSchema, { seconds: 10n ** 20n, nanos: 0 }),
    }),
  ],
  [
    "out-of-range calculated seconds",
    bare({
      calculatedAt: create(TimestampSchema, {
        seconds: -(10n ** 20n),
        nanos: 0,
      }),
    }),
  ],
])(
  "classifies a completed response with %s as Unavailable and keeps refreshing",
  async (_name, body) => {
    fakeClock();
    const { handler, transport } = scripted([{ body }]);
    mountSummary(transport);
    await advance(301);
    expect(handler).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(screen.getByText(CalendarPreviewState.Unavailable)).toBeTruthy();
    expect(screen.getByText("no-date")).toBeTruthy();
    expect(firstRunText()).toBe(
      "Preview unavailable; saving determines the next run.",
    );
    expect(screen.getByText("FALLBACK-RECURRENCE")).toBeTruthy();
    await advance(60_000);
    expect(handler).toHaveBeenCalledTimes(2);
    expect(screen.getByText(CalendarPreviewState.Unavailable)).toBeTruthy();
  },
);
it("keeps Calculating only while the initial read is in flight and Ready for complete evidence", async () => {
  fakeClock();
  const { transport } = scripted([{ delayMs: 1000, body: response }]);
  mountSummary(transport);
  await advance(300);
  expect(screen.getByText(CalendarPreviewState.Calculating)).toBeTruthy();
  await advance(1001);
  expect(screen.getByText(CalendarPreviewState.Ready)).toBeTruthy();
  expect(screen.queryByText("FALLBACK-RECURRENCE")).toBeNull();
});
