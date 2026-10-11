// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
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
  ).toBe("in 2 days");
  const or = create(ScheduleCalendarConstraintsSchema, {
    ...constraints,
    daysOfMonth: [1],
    weekdays: [1],
    dayMatch: ScheduleCalendarDayMatch.OR,
  });
  expect(calendarRecurrence(or)).toContain("day of month 1 or weekday Monday");
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
  vi.useFakeTimers();
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
    await vi.advanceTimersByTimeAsync(1100);
  });
  expect(preview).toHaveBeenCalledTimes(2);
});
