// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ScheduleCalendarSchema, ScheduleDayMatch, ScheduleService } from "@delinoio/delidev-api-client";
import { CalendarPreviewState, calendarDate, calendarSentence, ScheduleCalendarSummary, useScheduleCalendar, validCalendar } from "./schedule-calendar";
import { i18n } from "./localization";
import { DateFormatPreference } from "./timestamp-format";

const range = (first: number,last: number) => Array.from({length:last-first+1},(_,index) => index+first);
const calendar = () => create(ScheduleCalendarSchema,{minutes:[0],hours:[9],daysOfMonth:range(1,31),months:range(1,12),weekdays:[1,2,3,4,5],dayMatch:ScheduleDayMatch.AND});
const preview = () => ({boundary:"2026-10-10T09:00:00Z",nextRunAt:"2026-10-12T09:00:00Z",calendar:calendar()});
function fixture(handler: () => unknown = preview) {
  const read = vi.fn(handler);
  const transport = createRouterTransport(router => router.service(ScheduleService,{previewScheduleCalendar:read as typeof preview}));
  const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
  function Surface({cron="0 9 * * 1-5",timezone="UTC",enabled=true,active=true}:{cron?:string;timezone?:string;enabled?:boolean;active?:boolean}) {
    const observation = useScheduleCalendar(cron,timezone,active,false);
    return <><input aria-label="Keep focus" defaultValue="Draft"/><ScheduleCalendarSummary preview={observation} cron={cron} timezone={timezone} enabled={enabled} fallback="Original preset summary"/></>;
  }
  const view = (props: Parameters<typeof Surface>[0] = {}) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Surface {...props}/></QueryClientProvider></TransportProvider>;
  return {read,client,view,Surface};
}

it("renders approved recurrence, activation and server-relative first run without moving focus", async () => {
 const f = fixture(); const rendered = render(f.view());
 const input = screen.getByLabelText("Keep focus"); input.focus();
 expect(screen.getByText("Calculating the first run preview…")).toBeTruthy();
 expect(await screen.findByText("Every Monday to Friday at 9:00 AM")).toBeTruthy();
 expect(screen.getByText("After you save this schedule")).toBeTruthy();
 expect(screen.getByText(/Monday, October 12, 2026/)).toBeTruthy();
 expect(screen.getByText(/UTC · In 2 days/)).toBeTruthy();
 expect(document.activeElement).toBe(input);
 expect(rendered.container.querySelector("details")?.open).toBe(false);
 rendered.rerender(f.view({enabled:false}));
 expect(screen.getByText("After you enable this schedule")).toBeTruthy();
 expect(screen.getByText("No automatic run is scheduled.")).toBeTruthy();
 expect(screen.queryByText(/Monday, October 12, 2026/)).toBeNull();
 await act(async () => { await i18n.changeLanguage("ko"); });
 expect(screen.getByText("자동 실행이 예약되지 않았습니다.")).toBeTruthy();
 expect(f.read).toHaveBeenCalledOnce(); f.client.clear();
});

it.each([Code.Unimplemented,Code.InvalidArgument,Code.Unavailable])("keeps draft and hides dates on typed error %s", async code => {
 const f = fixture(() => { throw new ConnectError("No preview",code); });render(f.view());
 await waitFor(() => expect(f.read).toHaveBeenCalledOnce());
 const message = code === Code.Unimplemented ? /does not support previews/ : code === Code.InvalidArgument ? /Enter a valid five-field/ : /Preview unavailable/;
 expect(await screen.findByText(message)).toBeTruthy();
 expect(screen.queryByText(/October 12/)).toBeNull();
 expect(screen.getByText("Original preset summary")).toBeTruthy(); f.client.clear();
});

it("hides an obsolete date immediately and rejects a late changed-input reply", async () => {
 let finish!: (value: ReturnType<typeof preview>) => void;
 const f = fixture(() => new Promise(resolve => {finish=resolve;}));const rendered = render(f.view());
 await waitFor(() => expect(f.read).toHaveBeenCalledOnce());
 rendered.rerender(f.view({cron:"0 10 * * *"}));
 await act(async () => finish(preview()));
 expect(screen.queryByText(/October 12/)).toBeNull();
 rendered.rerender(f.view({active:false}));
 expect(screen.queryByText(/October 12/)).toBeNull(); f.client.clear();
});

it("renders AND/OR constraints faithfully and rejects malformed server values", async () => {
 const c = create(ScheduleCalendarSchema,{minutes:[0,30],hours:[8,9,10],daysOfMonth:[1],months:[1,3],weekdays:[1],dayMatch:ScheduleDayMatch.OR});
 expect(validCalendar(c)).toBe(true);
 expect(calendarSentence(c)).toContain("day of month 1 or Monday");
 expect(calendarSentence(c)).toContain("January and March");
 expect(validCalendar({...c,minutes:[30,0]})).toBe(false);
 expect(validCalendar({...c,weekdays:[7]})).toBe(false);
 const f = fixture(() => ({...preview(),calendar:{...calendar(),dayMatch:ScheduleDayMatch.UNSPECIFIED}}));render(f.view());
 expect(await screen.findByText(/Preview unavailable/)).toBeTruthy();
 expect(screen.queryByText(/October 12/)).toBeNull(); f.client.clear();
});

it("retains explicit schedule timezone in local date presentation", () => {
 render(<ScheduleCalendarSummary preview={{state:CalendarPreviewState.Ready,sample:{calendar:calendar(),boundary:Date.parse("2026-09-25T00:00:00Z"),next:Date.parse("2026-09-26T00:00:00Z"),received:performance.now()}}} cron="0 9 * * *" timezone="Asia/Seoul" enabled fallback=""/>);
 expect(screen.getByText(/Saturday, September 26, 2026 at 9:00 AM/)).toBeTruthy();
 expect(screen.getAllByText(/Asia\/Seoul/).length).toBe(2);
});

it("refreshes on the minute and due boundary while suspending inactive reads", async () => {
 vi.useFakeTimers();
 try {
  const f = fixture();const rendered = render(f.view());
  await act(async () => { await vi.advanceTimersByTimeAsync(301); });
  expect(f.read).toHaveBeenCalledOnce();
  await act(async () => { await vi.advanceTimersByTimeAsync(60001); });
  expect(f.read).toHaveBeenCalledTimes(2);
  rendered.rerender(f.view({active:false}));
  await act(async () => { await vi.advanceTimersByTimeAsync(120000); });
  expect(f.read).toHaveBeenCalledTimes(2);f.client.clear();
 } finally { vi.useRealTimers(); }
});

it("refreshes at an imminent server-owned occurrence", async () => {
 vi.useFakeTimers();
 try {
  const f = fixture(() => ({...preview(),boundary:"2026-10-12T08:59:59.500Z"}));render(f.view());
  await act(async () => { await vi.advanceTimersByTimeAsync(301); });
  expect(f.read).toHaveBeenCalledOnce();
  await act(async () => { await vi.advanceTimersByTimeAsync(501); });
  expect(f.read.mock.calls.length).toBeGreaterThanOrEqual(2);f.client.clear();
 } finally { vi.useRealTimers(); }
});


it("rejects retained estimates when the original connection is replaced", async () => {
 const f = fixture();const rendered = render(f.view());
 expect(await screen.findByText(/Monday, October 12, 2026/)).toBeTruthy();
 const otherRead = vi.fn(() => new Promise<ReturnType<typeof preview>>(() => {}));
 const replacement = createRouterTransport(router => router.service(ScheduleService,{previewScheduleCalendar:otherRead}));
 rendered.rerender(<TransportProvider transport={replacement}><QueryClientProvider client={f.client}><f.Surface/></QueryClientProvider></TransportProvider>);
 expect(screen.queryByText(/Monday, October 12, 2026/)).toBeNull();
 await waitFor(() => expect(otherRead).toHaveBeenCalledOnce());
 expect(screen.queryByText(/Monday, October 12, 2026/)).toBeNull();f.client.clear();
});

it("localizes server calendar semantics in Korean without parsing cron", async () => {
 await act(async () => { await i18n.changeLanguage("ko"); });
 expect(calendarSentence(calendar())).toContain("매주 월요일부터 금요일까지");
 const c = create(ScheduleCalendarSchema,{minutes:[0],hours:[9],daysOfMonth:[1],months:[2],weekdays:[1],dayMatch:ScheduleDayMatch.OR});
 expect(calendarSentence(c)).toContain("날짜 1 또는 월요일");
});


it("respects the selected date format and rejects unavailable renderer zones", () => {
 const instant = Date.parse("2026-10-12T09:00:00Z");
 expect(calendarDate(instant,"UTC",DateFormatPreference.Ymd)).toContain("2026-10-12");
 expect(calendarDate(instant,"UTC",DateFormatPreference.Dmy)).toContain("12/10/2026");
 expect(calendarDate(instant,"Unavailable/Zone",DateFormatPreference.System)).toBeUndefined();
 render(<ScheduleCalendarSummary preview={{state:CalendarPreviewState.Ready,sample:{calendar:calendar(),boundary:instant-86400000,next:instant,received:performance.now()}}} cron="0 9 * * *" timezone="Unavailable/Zone" enabled fallback="Original preset summary"/>);
 expect(screen.getByText(/Preview unavailable/)).toBeTruthy();
 expect(screen.queryByText(/October 12/)).toBeNull();
});
