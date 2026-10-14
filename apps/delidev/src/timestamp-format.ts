// SPDX-License-Identifier: Apache-2.0
import { copy, displayLocale } from "./localization";
import { rfc3339Instant } from "./rfc3339";

export enum DateFormatPreference {
  System = "system",
  Ymd = "ymd",
  Mdy = "mdy",
  Dmy = "dmy",
}
export enum TimestampMode {
  Ordinary = "ordinary",
  Absolute = "absolute",
  Exact = "exact",
  QuotaCountdown = "quota-countdown",
  /** Elapsed time at every age: now, minutes, hours, days, weeks, months, years. */
  Relative = "relative",
}
const MINUTE = 60000,
  HOUR = 3600000,
  DAY = 86400000,
  WEEK = 7 * DAY,
  MONTH = 30 * DAY,
  YEAR = 365 * DAY;
/** Closed elapsed-time units for the Relative mode. Each unit lasts until the
 * next band starts, so label boundaries are exact multiples of the band size. */
enum RelativeUnit {
  Now = "now",
  Minute = "minute",
  Hour = "hour",
  Day = "day",
  Week = "week",
  Month = "month",
  Year = "year",
}
const relativeBands: { unit: RelativeUnit; size: number; end: number }[] = [
  { unit: RelativeUnit.Now, size: MINUTE, end: MINUTE },
  { unit: RelativeUnit.Minute, size: MINUTE, end: HOUR },
  { unit: RelativeUnit.Hour, size: HOUR, end: DAY },
  { unit: RelativeUnit.Day, size: DAY, end: WEEK },
  { unit: RelativeUnit.Week, size: WEEK, end: MONTH },
  { unit: RelativeUnit.Month, size: MONTH, end: YEAR },
  { unit: RelativeUnit.Year, size: YEAR, end: Infinity },
];
/** Nonnegative age only; callers keep their own future-value fallback. */
function relativeLabel(age: number): string {
  const band = relativeBands.find((item) => age < item.end)!;
  if (band.unit === RelativeUnit.Now) return copy("timestamp.now");
  const count = Math.floor(age / band.size),
    one = count === 1;
  const key = {
    [RelativeUnit.Minute]: one ? "timestamp.minute" : "timestamp.minutes",
    [RelativeUnit.Hour]: one ? "timestamp.hour" : "timestamp.hours",
    [RelativeUnit.Day]: one ? "timestamp.day" : "timestamp.days",
    [RelativeUnit.Week]: one ? "timestamp.week" : "timestamp.weeks",
    [RelativeUnit.Month]: one ? "timestamp.month" : "timestamp.months",
    [RelativeUnit.Year]: one ? "timestamp.year" : "timestamp.years",
  } as const;
  return copy(key[band.unit as Exclude<RelativeUnit, RelativeUnit.Now>], {
    count,
  });
}
export interface TimestampOptions {
  preference?: DateFormatPreference;
  mode?: TimestampMode;
  now?: number;
  timeZone?: string;
}

/** Validate source wall time before converting its instant. Date.parse alone
 * normalizes impossible calendar dates and cannot establish valid evidence. */
export function timestampInstant(value: string): number | undefined {
  return rfc3339Instant(value);
}

/** Render the original RFC3339 instant as `YYYY-MM-DD HH:mm:ss[.fraction] UTC`.
 * The fraction digits are copied from the source so nanosecond evidence is
 * never rounded; offsets convert only the whole-second wall time. Invalid
 * values return undefined so callers never present unverified evidence. */
export function formatUtcInstant(value: string): string | undefined {
  const instant = timestampInstant(value);
  if (instant === undefined) return;
  const wall = new Date(Math.floor(instant / 1000) * 1000)
    .toISOString()
    .slice(0, 19)
    .replace("T", " ");
  const fraction = /^[^.]*\.(\d+)/.exec(value)?.[1];
  return `${wall}${fraction ? `.${fraction}` : ""} UTC`;
}

export function nextTimestampTransition(
  instant: number,
  now: number,
  mode = TimestampMode.Ordinary,
): number | undefined {
  if (mode === TimestampMode.QuotaCountdown && instant > now) {
    const remaining = instant - now;
    if (remaining < 60000) return instant;
    const unit = remaining >= 86400000 ? 3600000 : 60000;
    // Floored countdowns change just after an exact unit boundary. Scheduling
    // that next millisecond avoids a zero-delay loop and keeps expiry exact.
    return Math.min(instant, instant - Math.floor(remaining / unit) * unit + 1);
  }
  const age = now - instant;
  if (age < 0) return instant;
  if (mode === TimestampMode.Relative) {
    const band = relativeBands.find((item) => age < item.end)!;
    // Week and month bands end off a multiple of their unit (30 days is not a
    // multiple of 7 days), so the band end caps the next unit boundary.
    return (
      instant +
      Math.min(band.end, (Math.floor(age / band.size) + 1) * band.size)
    );
  }
  if (age >= 86400000) return;
  const unit = age < 3600000 ? 60000 : 3600000;
  return instant + (Math.floor(age / unit) + 1) * unit;
}

/** Complete sentences keep plural choices and Korean unit ordering in catalogs. */
function quotaCountdown(remaining: number): string {
  const days = Math.floor(remaining / 86400000),
    hours = Math.floor(remaining / 3600000) % 24,
    minutes = Math.floor(remaining / 60000) % 60;
  if (days)
    return copy(
      hours
        ? days === 1
          ? hours === 1
            ? "quota-countdown.resetDayHour"
            : "quota-countdown.resetDayHours"
          : hours === 1
            ? "quota-countdown.resetDaysHour"
            : "quota-countdown.resetDaysHours"
        : days === 1
          ? "quota-countdown.resetDay"
          : "quota-countdown.resetDays",
      { days, hours },
    );
  if (hours)
    return copy(
      minutes
        ? hours === 1
          ? minutes === 1
            ? "quota-countdown.resetHourMinute"
            : "quota-countdown.resetHourMinutes"
          : minutes === 1
            ? "quota-countdown.resetHoursMinute"
            : "quota-countdown.resetHoursMinutes"
        : hours === 1
          ? "quota-countdown.resetHour"
          : "quota-countdown.resetHours",
      { hours, minutes },
    );
  return copy(
    minutes
      ? minutes === 1
        ? "quota-countdown.resetMinute"
        : "quota-countdown.resetMinutes"
      : "quota-countdown.resetSoon",
    { minutes },
  );
}

export function formatTimestampLabel(
  value: string,
  {
    preference = DateFormatPreference.System,
    mode = TimestampMode.Ordinary,
    now = Date.now(),
    timeZone,
  }: TimestampOptions = {},
): string {
  const instant = timestampInstant(value);
  if (instant === undefined || mode === TimestampMode.Exact) return value;
  const age = now - instant;
  if (mode === TimestampMode.QuotaCountdown && age < 0)
    return quotaCountdown(-age);
  if (mode === TimestampMode.Relative && age >= 0) return relativeLabel(age);
  if (
    (mode === TimestampMode.Ordinary ||
      mode === TimestampMode.QuotaCountdown) &&
    age >= 0 &&
    age < 86400000
  ) {
    if (age < 60000) return copy("timestamp.now");
    const count = Math.floor(age / (age < 3600000 ? 60000 : 3600000));
    return copy(
      age < 3600000
        ? count === 1
          ? "timestamp.minute"
          : "timestamp.minutes"
        : count === 1
          ? "timestamp.hour"
          : "timestamp.hours",
      { count },
    );
  }
  const date = new Date(instant),
    locale = displayLocale();
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(date);
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((item) => item.type === type)?.value ?? "";
  const day =
    preference === DateFormatPreference.Ymd
      ? `${part("year")}-${part("month")}-${part("day")}`
      : preference === DateFormatPreference.Mdy
        ? `${part("month")}/${part("day")}/${part("year")}`
        : preference === DateFormatPreference.Dmy
          ? `${part("day")}/${part("month")}/${part("year")}`
          : new Intl.DateTimeFormat(locale, {
              timeZone,
              year: "numeric",
              month: "short",
              day: "numeric",
            }).format(date);
  const clockParts = new Intl.DateTimeFormat(locale, {
    timeZone,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
    timeZoneName: "short",
  }).formatToParts(date);
  const clockPart = (type: Intl.DateTimeFormatPartTypes) =>
    clockParts.find((item) => item.type === type)?.value ?? "";
  // Construct the clock from parts: Korean uses unit words, so replacing a
  // colon-based substring would silently drop the original source fraction.
  const fraction = /\.(\d+)(?:Z|[+-])/.exec(value)?.[1];
  const clock = `${clockPart("hour")}:${clockPart("minute")}:${clockPart("second")}${fraction ? `.${fraction}` : ""} ${clockPart("timeZoneName")}`;
  return `${day}, ${clock}`;
}
