// SPDX-License-Identifier: Apache-2.0
import { copy, displayLocale } from "./localization";

export enum DateFormatPreference { System = "system", Ymd = "ymd", Mdy = "mdy", Dmy = "dmy" }
export enum TimestampMode { Ordinary = "ordinary", Absolute = "absolute", Exact = "exact", QuotaCountdown = "quota-countdown" }
export interface TimestampOptions { preference?: DateFormatPreference; mode?: TimestampMode; now?: number; timeZone?: string }

/** Validate source wall time before converting its instant. Date.parse alone
 * normalizes impossible calendar dates and cannot establish valid evidence. */
export function timestampInstant(value: string): number | undefined {
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match) return;
  const wall = new Date(`${match[1]}T${match[2]}Z`);
  if (!Number.isFinite(wall.getTime()) || wall.toISOString().slice(0, 19) !== `${match[1]}T${match[2]}`) return;
  if (match[4] !== "Z" && (Number(match[4]!.slice(1, 3)) > 23 || Number(match[4]!.slice(4)) > 59)) return;
  const instant = Date.parse(value);
  return Number.isFinite(instant) ? instant : undefined;
}

export function nextTimestampTransition(instant: number, now: number, mode = TimestampMode.Ordinary): number | undefined {
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
  if (age >= 86400000) return;
  const unit = age < 3600000 ? 60000 : 3600000;
  return instant + (Math.floor(age / unit) + 1) * unit;
}


/** Complete sentences keep plural choices and Korean unit ordering in catalogs. */
function quotaCountdown(remaining: number): string {
  const days = Math.floor(remaining / 86400000), hours = Math.floor(remaining / 3600000) % 24, minutes = Math.floor(remaining / 60000) % 60;
  if (days) return copy(hours ? days === 1 ? hours === 1 ? "quota-countdown.resetDayHour" : "quota-countdown.resetDayHours" : hours === 1 ? "quota-countdown.resetDaysHour" : "quota-countdown.resetDaysHours" : days === 1 ? "quota-countdown.resetDay" : "quota-countdown.resetDays", { days, hours });
  if (hours) return copy(minutes ? hours === 1 ? minutes === 1 ? "quota-countdown.resetHourMinute" : "quota-countdown.resetHourMinutes" : minutes === 1 ? "quota-countdown.resetHoursMinute" : "quota-countdown.resetHoursMinutes" : hours === 1 ? "quota-countdown.resetHour" : "quota-countdown.resetHours", { hours, minutes });
  return copy(minutes ? minutes === 1 ? "quota-countdown.resetMinute" : "quota-countdown.resetMinutes" : "quota-countdown.resetSoon", { minutes });
}

export function formatTimestampLabel(value: string, { preference = DateFormatPreference.System, mode = TimestampMode.Ordinary, now = Date.now(), timeZone }: TimestampOptions = {}): string {
  const instant = timestampInstant(value);
  if (instant === undefined || mode === TimestampMode.Exact) return value;
  const age = now - instant;
  if (mode === TimestampMode.QuotaCountdown && age < 0) return quotaCountdown(-age);
  if ((mode === TimestampMode.Ordinary || mode === TimestampMode.QuotaCountdown) && age >= 0 && age < 86400000) {
    if (age < 60000) return copy("timestamp.now");
    const count = Math.floor(age / (age < 3600000 ? 60000 : 3600000));
    return copy(age < 3600000 ? count === 1 ? "timestamp.minute" : "timestamp.minutes" : count === 1 ? "timestamp.hour" : "timestamp.hours", { count });
  }
  const date = new Date(instant), locale = displayLocale();
  const parts = new Intl.DateTimeFormat("en-US", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(date);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find(item => item.type === type)?.value ?? "";
  const day = preference === DateFormatPreference.Ymd ? `${part("year")}-${part("month")}-${part("day")}`
    : preference === DateFormatPreference.Mdy ? `${part("month")}/${part("day")}/${part("year")}`
      : preference === DateFormatPreference.Dmy ? `${part("day")}/${part("month")}/${part("year")}`
        : new Intl.DateTimeFormat(locale, { timeZone, year: "numeric", month: "short", day: "numeric" }).format(date);
  const clockParts = new Intl.DateTimeFormat(locale, { timeZone, hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23", timeZoneName: "short" }).formatToParts(date);
  const clockPart = (type: Intl.DateTimeFormatPartTypes) => clockParts.find(item => item.type === type)?.value ?? "";
  // Construct the clock from parts: Korean uses unit words, so replacing a
  // colon-based substring would silently drop the original source fraction.
  const fraction = /\.(\d+)(?:Z|[+-])/.exec(value)?.[1];
  const clock = `${clockPart("hour")}:${clockPart("minute")}:${clockPart("second")}${fraction ? `.${fraction}` : ""} ${clockPart("timeZoneName")}`;
  return `${day}, ${clock}`;
}
