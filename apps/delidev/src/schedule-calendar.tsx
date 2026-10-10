// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { ScheduleDayMatch, ScheduleQuery, type ScheduleCalendar } from "@delinoio/delidev-api-client";
import { copy, displayLocale, useLocale } from "./localization";
import { useDateFormat } from "./date-format";
import { DateFormatPreference, formatTimestampLabel, TimestampMode, timestampInstant } from "./timestamp-format";
import { Disclosure, DisclosureSummary } from "./disclosure";

export enum CalendarPreviewState { Calculating = "calculating", Ready = "ready", Invalid = "invalid", Unavailable = "unavailable", Unsupported = "unsupported" }
const valuesValid = (values: number[], min: number, max: number) => values.length > 0 && values.length <= max-min+1 && values.every((value,index) => Number.isInteger(value) && value >= min && value <= max && (index === 0 || value > values[index-1]!));
export function validCalendar(calendar?: ScheduleCalendar): calendar is ScheduleCalendar {
  return Boolean(calendar && valuesValid(calendar.minutes,0,59) && valuesValid(calendar.hours,0,23) && valuesValid(calendar.daysOfMonth,1,31) && valuesValid(calendar.months,1,12) && valuesValid(calendar.weekdays,0,6) && [ScheduleDayMatch.AND,ScheduleDayMatch.OR].includes(calendar.dayMatch));
}

/** One original connection/input owner shared by Repeat and Review. */
export function useScheduleCalendar(cron: string, timezone: string, active: boolean, invalid: boolean) {
  const transport = useTransport();
  const [input,setInput] = useState<{ cron: string; timezone: string; transport: typeof transport }>();
  const permitted = !invalid && Boolean(cron.trim() && timezone.trim()) && cron.length <= 512 && timezone.length <= 256;
  useEffect(() => {
    if (!active || !permitted) { setInput(undefined); return; }
    const timer = setTimeout(() => setInput({cron,timezone,transport}),300);
    return () => clearTimeout(timer);
  },[cron,timezone,transport,active,permitted]);
  const exact = active && permitted && input?.cron === cron && input?.timezone === timezone && input?.transport === transport;
  const result = useQuery(ScheduleQuery.previewScheduleCalendar,{cron:input?.cron ?? "",timezone:input?.timezone ?? ""},{enabled:exact,retry:false,staleTime:0});
  const lastSample = useRef<{ cron: string; timezone: string; transport: typeof transport; boundary: number; received: number } | undefined>(undefined);
  const sample = useMemo(() => {
    const data = result.data, boundary = data && timestampInstant(data.boundary), next = data && timestampInstant(data.nextRunAt);
    if (!data || boundary === undefined || next === undefined || !data.boundary.endsWith("Z") || !data.nextRunAt.endsWith("Z") || next <= boundary || !validCalendar(data.calendar)) return;
    const previous = lastSample.current;
    const same = previous?.cron === input?.cron && previous?.timezone === input?.timezone && previous?.transport === transport;
    if (same && previous && boundary < previous.boundary) return;
    // Repeated identical server boundaries cannot restart the elapsed clock and
    // make an expired estimate appear future again.
    const received = same && previous && boundary === previous.boundary ? previous.received : performance.now();
    lastSample.current = {cron:input?.cron ?? "",timezone:input?.timezone ?? "",transport,boundary,received};
    return {calendar:data.calendar,boundary,next,received};
  },[result.data,result.dataUpdatedAt,transport,input]);
  // Refresh the estimate, not a countdown. The browser's wall clock and zone
  // never determine either the occurrence or the server calculation boundary.
  useEffect(() => {
    if (!exact || result.isFetching) return;
    const remaining = sample && !result.error ? sample.next-sample.boundary-(performance.now()-sample.received) : 60000;
    const timer = setTimeout(() => void result.refetch(),remaining > 0 ? Math.max(1,Math.min(60000,remaining)) : 60000);
    return () => clearTimeout(timer);
  },[exact,sample,result.isFetching,result.error,result.refetch]);
  const error = result.error && ConnectError.from(result.error);
  const state = !permitted ? CalendarPreviewState.Invalid : !exact || result.isFetching || !result.data && !error ? CalendarPreviewState.Calculating
    : error?.code === Code.Unimplemented ? CalendarPreviewState.Unsupported : error?.code === Code.InvalidArgument ? CalendarPreviewState.Invalid : error || !sample ? CalendarPreviewState.Unavailable : CalendarPreviewState.Ready;
  return {state,sample:state === CalendarPreviewState.Ready ? sample : undefined};
}
export type ScheduleCalendarPreview = ReturnType<typeof useScheduleCalendar>;

function clock(hour: number, minute: number) {
  return new Intl.DateTimeFormat(displayLocale(),{timeZone:"UTC",hour:"numeric",minute:"2-digit"}).format(new Date(Date.UTC(2026,0,1,hour,minute)));
}
function weekday(day: number) {
  return new Intl.DateTimeFormat(displayLocale(),{timeZone:"UTC",weekday:"long"}).format(new Date(Date.UTC(2026,0,4+day)));
}
const list = (values: string[]) => new Intl.ListFormat(displayLocale(),{style:"long",type:"conjunction"}).format(values);
export function calendarSentence(calendar: ScheduleCalendar): string {
  const allDays = calendar.daysOfMonth.length === 31, allMonths = calendar.months.length === 12, allWeekdays = calendar.weekdays.length === 7;
  const singleTime = calendar.hours.length === 1 && calendar.minutes.length === 1;
  if (singleTime && allDays && allMonths && calendar.dayMatch === ScheduleDayMatch.AND) {
    const time = clock(calendar.hours[0]!,calendar.minutes[0]!);
    if (allWeekdays) return copy("schedule-calendar.daily",{time});
    if (calendar.weekdays.join(",") === "1,2,3,4,5") return copy("schedule-calendar.weekdays",{time});
    return copy("schedule-calendar.weekly",{days:list(calendar.weekdays.map(weekday)),time});
  }
  const time = singleTime ? clock(calendar.hours[0]!,calendar.minutes[0]!) : copy("schedule-calendar.timeConstraints",{hours:calendar.hours.length === 24 ? copy("schedule-calendar.everyHour") : list(calendar.hours.map(String)),minutes:calendar.minutes.length === 60 ? copy("schedule-calendar.everyMinute") : list(calendar.minutes.map(String))});
  const months = allMonths ? copy("schedule-calendar.everyMonth") : list(calendar.months.map(month => new Intl.DateTimeFormat(displayLocale(),{timeZone:"UTC",month:"long"}).format(new Date(Date.UTC(2026,month-1,1)))));
  const days = copy(calendar.dayMatch === ScheduleDayMatch.OR ? "schedule-calendar.daysOr" : "schedule-calendar.daysAnd",{dates:allDays ? copy("schedule-calendar.everyDayOfMonth") : list(calendar.daysOfMonth.map(String)),weekdays:allWeekdays ? copy("schedule-calendar.everyWeekday") : list(calendar.weekdays.map(weekday))});
  return copy("schedule-calendar.constraints",{time,months,days});
}
function relative(remaining: number): string {
  const unit = remaining >= 86400000 ? "day" : remaining >= 3600000 ? "hour" : "minute";
  const divisor = unit === "day" ? 86400000 : unit === "hour" ? 3600000 : 60000;
  return copy(unit === "day" ? "schedule-calendar.inDays" : unit === "hour" ? "schedule-calendar.inHours" : "schedule-calendar.inMinutes",{count:Math.max(1,Math.ceil(remaining/divisor))});
}
export function calendarDate(next: number, timezone: string, preference: DateFormatPreference): string | undefined {
  try {
    return preference === DateFormatPreference.System
      ? new Intl.DateTimeFormat(displayLocale(),{timeZone:timezone,weekday:"long",year:"numeric",month:"long",day:"numeric",hour:"numeric",minute:"2-digit"}).format(new Date(next))
      : `${new Intl.DateTimeFormat(displayLocale(),{timeZone:timezone,weekday:"long"}).format(new Date(next))}, ${formatTimestampLabel(new Date(next).toISOString(),{preference,mode:TimestampMode.Absolute,timeZone:timezone})}`;
  } catch { return undefined; } // An unavailable renderer zone cannot fabricate a local date.
}
export function ScheduleCalendarSummary({ preview, cron, timezone, enabled, fallback }: { preview: ScheduleCalendarPreview; cron: string; timezone: string; enabled: boolean; fallback: string }) {
  useLocale();
  const preference = useDateFormat();
  const sample = preview.sample;
  const remaining = sample ? sample.next-sample.boundary-(performance.now()-sample.received) : 0;
  const date = sample && remaining > 0 ? calendarDate(sample.next,timezone,preference) : undefined;
  const ready = Boolean(date);
  const status = preview.state === CalendarPreviewState.Invalid ? "schedule-calendar.invalid" : preview.state === CalendarPreviewState.Unsupported ? "schedule-calendar.unsupported" : preview.state === CalendarPreviewState.Unavailable || sample && !ready ? "schedule-calendar.unavailable" : "schedule-calendar.calculating";
  return <div className="schedule-creation-preview schedule-calendar-summary"><dl aria-live="polite" aria-atomic="true">
    <dt>{copy("schedule-calendar.repeats")}</dt><dd>{ready && sample ? calendarSentence(sample.calendar) : fallback}<small>{timezone}</small></dd>
    <dt>{copy("schedule-calendar.starts")}</dt><dd>{copy(enabled ? "schedule-calendar.afterSave" : "schedule-calendar.afterEnable")}</dd>
    <dt>{copy("schedule-calendar.firstRun")}</dt><dd>{!enabled ? copy("schedule-calendar.paused") : ready ? <>{date}<small>{timezone} · {relative(remaining)}</small></> : copy(status)}</dd>
  </dl>{!enabled && !ready ? <p role="status">{copy(status)}</p> : null}<p>{copy("schedule-calendar.provisional")}</p>{cron ? <Disclosure><DisclosureSummary>{copy("schedule-calendar.details")}</DisclosureSummary><code>{cron}</code></Disclosure> : null}</div>;
}
