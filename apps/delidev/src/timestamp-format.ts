// SPDX-License-Identifier: Apache-2.0
import { copy, displayLocale } from "./localization";

export enum DateFormatPreference { System = "system", Ymd = "ymd", Mdy = "mdy", Dmy = "dmy" }
export enum TimestampMode { Ordinary = "ordinary", Absolute = "absolute", Exact = "exact" }
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

export function nextTimestampTransition(instant: number, now: number): number | undefined {
  const age = now - instant;
  if (age < 0) return instant;
  if (age >= 86400000) return;
  const unit = age < 3600000 ? 60000 : 3600000;
  return instant + (Math.floor(age / unit) + 1) * unit;
}

export function formatTimestampLabel(value: string, { preference = DateFormatPreference.System, mode = TimestampMode.Ordinary, now = Date.now(), timeZone }: TimestampOptions = {}): string {
  const instant = timestampInstant(value);
  if (instant === undefined || mode === TimestampMode.Exact) return value;
  const age = now - instant;
  if (mode === TimestampMode.Ordinary && age >= 0 && age < 86400000) {
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
