// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { Code, ConnectError, type Transport } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import {
  ScheduleCalendarDayMatch,
  ScheduleQuery,
  type ScheduleCalendarConstraints,
  type PreviewScheduleCalendarResponse,
} from "@delinoio/delidev-api-client";
import { copy, displayLocale, useLocale } from "./localization";
import { useDateFormat } from "./date-format";
import {
  DateFormatPreference,
  formatTimestampLabel,
  TimestampMode,
} from "./timestamp-format";
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";

export enum CalendarPreviewState {
  Calculating = "calculating",
  Ready = "ready",
  Invalid = "invalid",
  Unavailable = "unavailable",
  Unsupported = "unsupported",
}
interface PreviewInput {
  cron: string;
  timezone: string;
  transport: Transport;
}
interface PreviewSnapshot extends PreviewInput {
  data: PreviewScheduleCalendarResponse;
  receivedAt: number;
}

export function useScheduleCalendarPreview(
  cron: string,
  timezone: string,
  active: boolean,
  valid: boolean,
) {
  const transport = useTransport();
  const [input, setInput] = useState<PreviewInput>();
  const [snapshot, setSnapshot] = useState<PreviewSnapshot>();
  const [elapsed, setElapsed] = useState(0);
  const sameInput =
    input?.cron === cron &&
    input.timezone === timezone &&
    input.transport === transport;
  useEffect(() => {
    setInput(undefined);
    setSnapshot(undefined);
    if (
      !active ||
      !valid ||
      !cron ||
      !timezone ||
      cron.length > 512 ||
      timezone.length > 256
    )
      return;
    const timer = setTimeout(
      () => setInput({ cron, timezone, transport }),
      300,
    );
    return () => clearTimeout(timer);
  }, [cron, timezone, transport, active, valid]);
  const enabled = active && valid && sameInput;
  const query = useQuery(
    ScheduleQuery.previewScheduleCalendar,
    { cron, timezone },
    {
      enabled,
      retry: false,
      staleTime: 0,
      gcTime: 0,
      refetchInterval: enabled ? 60000 : false,
      refetchOnWindowFocus: true,
    },
  );
  useEffect(() => {
    if (!enabled || !query.data || query.isFetching || query.isError) return;
    setSnapshot({
      cron,
      timezone,
      transport,
      data: query.data,
      receivedAt: performance.now(),
    });
    setElapsed(0);
  }, [
    enabled,
    query.data,
    query.dataUpdatedAt,
    query.isFetching,
    query.isError,
    cron,
    timezone,
    transport,
  ]);
  const current =
    enabled &&
    !query.isError &&
    snapshot?.cron === cron &&
    snapshot.timezone === timezone &&
    snapshot.transport === transport &&
    snapshot.data === query.data
      ? snapshot
      : undefined;
  const calculated = current?.data.calculatedAt
    ? timestampDate(current.data.calculatedAt).getTime()
    : undefined;
  const next = current?.data.nextRunAt
    ? timestampDate(current.data.nextRunAt).getTime()
    : undefined;
  useEffect(() => {
    if (
      !current ||
      calculated === undefined ||
      next === undefined ||
      !Number.isFinite(calculated) ||
      !Number.isFinite(next)
    )
      return;
    const remaining =
      next - calculated - (performance.now() - current.receivedAt);
    const timer = setTimeout(
      () => {
        setElapsed(performance.now() - current.receivedAt);
        if (remaining <= 60000) {
          setSnapshot(undefined);
          void query.refetch();
        }
      },
      Math.max(1, Math.min(60000, remaining)),
    );
    return () => clearTimeout(timer);
  }, [current, calculated, next, elapsed, query.refetch]);
  const estimate =
    calculated === undefined
      ? undefined
      : calculated +
        Math.max(elapsed, current ? performance.now() - current.receivedAt : 0);
  let state = CalendarPreviewState.Calculating;
  if (
    !valid ||
    !cron ||
    !timezone ||
    cron.length > 512 ||
    timezone.length > 256
  )
    state = CalendarPreviewState.Invalid;
  else if (enabled && query.error) {
    const code = ConnectError.from(query.error).code;
    state =
      code === Code.Unimplemented
        ? CalendarPreviewState.Unsupported
        : code === Code.InvalidArgument
          ? CalendarPreviewState.Invalid
          : CalendarPreviewState.Unavailable;
  } else if (
    current &&
    next !== undefined &&
    estimate !== undefined &&
    next > estimate &&
    current.data.constraints
  )
    state = CalendarPreviewState.Ready;
  return {
    state,
    constraints:
      state === CalendarPreviewState.Ready
        ? current?.data.constraints
        : undefined,
    next: state === CalendarPreviewState.Ready ? next : undefined,
    now: estimate,
  };
}
export type CalendarPreview = ReturnType<typeof useScheduleCalendarPreview>;

const full = (values: readonly number[], first: number, last: number) =>
  values.length === last - first + 1 &&
  values.every((value, index) => value === first + index);
const list = (values: readonly (string | number)[]) =>
  new Intl.ListFormat(displayLocale(), {
    style: "long",
    type: "conjunction",
  }).format(values.map(String));
const weekday = (day: number) =>
  new Intl.DateTimeFormat(displayLocale(), {
    weekday: "long",
    timeZone: "UTC",
  }).format(new Date(Date.UTC(2026, 0, 4 + day)));
const month = (value: number) =>
  new Intl.DateTimeFormat(displayLocale(), {
    month: "long",
    timeZone: "UTC",
  }).format(new Date(Date.UTC(2026, value - 1, 1)));
export function calendarRecurrence(
  calendar: ScheduleCalendarConstraints,
): string {
  const everyMonth = full(calendar.months, 1, 12),
    everyDate = full(calendar.daysOfMonth, 1, 31),
    everyWeekday = full(calendar.weekdays, 0, 6);
  const simpleTime =
    calendar.minutes.length === 1 && calendar.hours.length === 1;
  if (
    simpleTime &&
    everyMonth &&
    everyDate &&
    (calendar.dayMatch === ScheduleCalendarDayMatch.AND || everyWeekday)
  ) {
    const at = new Intl.DateTimeFormat(displayLocale(), {
      hour: "numeric",
      minute: "2-digit",
      timeZone: "UTC",
    }).format(
      new Date(Date.UTC(2026, 0, 1, calendar.hours[0], calendar.minutes[0])),
    );
    if (everyWeekday)
      return copy("schedule-creation.preview.daily", { time: at });
    if (calendar.weekdays.join(",") === "1,2,3,4,5")
      return copy("schedule-creation.preview.weekdays", { time: at });
    return copy("schedule-creation.preview.weekly", {
      days: list(calendar.weekdays.map(weekday)),
      time: at,
    });
  }
  const dates = everyDate
    ? copy("schedule-creation.preview.everyDate")
    : copy("schedule-creation.preview.dates", {
        values: list(calendar.daysOfMonth),
      });
  const days = everyWeekday
    ? copy("schedule-creation.preview.everyWeekday")
    : copy("schedule-creation.preview.days", {
        values: list(calendar.weekdays.map(weekday)),
      });
  const matching = copy(
    calendar.dayMatch === ScheduleCalendarDayMatch.OR
      ? "schedule-creation.preview.dayOr"
      : "schedule-creation.preview.dayAnd",
    { dates, days },
  );
  return copy("schedule-creation.preview.complex", {
    minutes: full(calendar.minutes, 0, 59)
      ? copy("schedule-creation.preview.everyMinute")
      : list(calendar.minutes.map((value) => String(value).padStart(2, "0"))),
    hours: full(calendar.hours, 0, 23)
      ? copy("schedule-creation.preview.everyHour")
      : list(calendar.hours.map((value) => String(value).padStart(2, "0"))),
    months: everyMonth
      ? copy("schedule-creation.preview.everyMonth")
      : list(calendar.months.map(month)),
    matching,
  });
}
export function calendarRelative(next: number, now: number) {
  const remaining = next - now;
  if (remaining < 60000) return copy("schedule-creation.preview.soon");
  const unit =
    remaining >= 86400000 ? "day" : remaining >= 3600000 ? "hour" : "minute";
  return new Intl.RelativeTimeFormat(displayLocale(), {
    numeric: "always",
  }).format(
    Math.floor(
      remaining /
        (unit === "day" ? 86400000 : unit === "hour" ? 3600000 : 60000),
    ),
    unit,
  );
}
export function ScheduleCalendarSummary({
  preview,
  cron,
  timezone,
  enabled,
  fallback,
}: {
  preview: CalendarPreview;
  cron: string;
  timezone: string;
  enabled: boolean;
  fallback: string;
}) {
  useLocale();
  const preference = useDateFormat();
  const next = preview.next;
  let date: string | undefined;
  if (next !== undefined) {
    try {
      const instant = new Date(next);
      date =
        preference === DateFormatPreference.System
          ? new Intl.DateTimeFormat(displayLocale(), {
              weekday: "long",
              year: "numeric",
              month: "long",
              day: "numeric",
              hour: "numeric",
              minute: "2-digit",
              timeZone: timezone,
            }).format(instant)
          : `${new Intl.DateTimeFormat(displayLocale(), { weekday: "long", timeZone: timezone }).format(instant)}, ${formatTimestampLabel(instant.toISOString(), { preference, mode: TimestampMode.Absolute, timeZone: timezone })}`;
    } catch {
      date = undefined;
    }
  }
  const status =
    preview.state === CalendarPreviewState.Unsupported
      ? copy("schedule-creation.preview.unsupported")
      : preview.state === CalendarPreviewState.Invalid
        ? copy("schedule-creation.preview.invalid")
        : preview.state === CalendarPreviewState.Unavailable ||
            (preview.state === CalendarPreviewState.Ready && !date)
          ? copy("schedule-creation.preview.unavailable")
          : copy("schedule-creation.preview.calculating");
  return (
    <section className="schedule-calendar-summary">
      <dl>
        <div>
          <dt>{copy("schedule-creation.preview.repeats")}</dt>
          <dd>
            {preview.constraints
              ? calendarRecurrence(preview.constraints)
              : fallback}
            <small>{timezone}</small>
          </dd>
        </div>
        <div>
          <dt>{copy("schedule-creation.preview.starts")}</dt>
          <dd>
            {copy(
              enabled
                ? "schedule-creation.preview.afterSave"
                : "schedule-creation.preview.afterEnable",
            )}
          </dd>
        </div>
        <div>
          <dt>{copy("schedule-creation.preview.first")}</dt>
          <dd role="status" aria-live="polite" aria-atomic="true">
            {!enabled ? (
              copy("schedule-creation.preview.paused")
            ) : date ? (
              <>
                {date}
                <small>
                  {timezone} · {calendarRelative(next!, preview.now!)}
                </small>
              </>
            ) : (
              status
            )}
          </dd>
        </div>
      </dl>
      <p>{copy("schedule-creation.preview.provisional")}</p>
      <Disclosure density={DisclosureDensity.Compact}>
        <DisclosureSummary>
          {copy("schedule-creation.preview.details")}
        </DisclosureSummary>
        <code>{cron}</code>
      </Disclosure>
    </section>
  );
}
