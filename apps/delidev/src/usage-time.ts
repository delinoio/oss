import { ProductError } from "./localization";
const dayMilliseconds = 86_400_000;
const wallClock = new Map<string, Intl.DateTimeFormat>();

function formatter(timeZone: string): Intl.DateTimeFormat {
  const cached = wallClock.get(timeZone);
  if (cached) return cached;
  const value = new Intl.DateTimeFormat("en-US-u-ca-gregory-nu-latn", { timeZone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
  wallClock.set(timeZone, value);
  return value;
}

function partsAt(milliseconds: number, timeZone: string): { year: number; month: number; day: number; hour: number; minute: number; second: number } {
  const values = new Map(formatter(timeZone).formatToParts(new Date(milliseconds)).map((part) => [part.type, part.value]));
  return { year: Number(values.get("year")), month: Number(values.get("month")), day: Number(values.get("day")), hour: Number(values.get("hour")), minute: Number(values.get("minute")), second: Number(values.get("second")) };
}

function offsetAt(milliseconds: number, timeZone: string): number {
  const wholeSecond = Math.floor(milliseconds / 1000) * 1000;
  const local = partsAt(wholeSecond, timeZone);
  return Date.UTC(local.year, local.month - 1, local.day, local.hour, local.minute, local.second) - wholeSecond;
}

export function detectDeviceTimeZone(): string {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (!zone || zone === "Local") return "UTC";
    formatter(zone).format(new Date(0));
    return zone;
  } catch {
    return "UTC";
  }
}

export function localDateTimeToUnixMs(value: string, timeZone: string): bigint {
  if (!value) return 0n;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/.exec(value);
  if (!match) throw new ProductError("validation.ede860515417");
  const [, yearText, monthText, dayText, hourText, minuteText, secondText = "0", fractionText = "0"] = match;
  const year = Number(yearText), month = Number(monthText), day = Number(dayText), hour = Number(hourText), minute = Number(minuteText), second = Number(secondText);
  const millisecond = Number(fractionText.padEnd(3, "0"));
  const wall = Date.UTC(year, month - 1, day, hour, minute, second, millisecond);
  const date = new Date(wall);
  if (year < 1970 || date.getUTCFullYear() !== year || date.getUTCMonth() + 1 !== month || date.getUTCDate() !== day || hour > 23 || minute > 59 || second > 59) throw new ProductError("validation.ede860515417");

  try {
    const offsets = new Set([offsetAt(wall - dayMilliseconds, timeZone), offsetAt(wall, timeZone), offsetAt(wall + dayMilliseconds, timeZone)]);
    const matches: number[] = [];
    for (const offset of offsets) {
      const candidate = wall - offset;
      const local = partsAt(candidate, timeZone);
      if (local.year === year && local.month === month && local.day === day && local.hour === hour && local.minute === minute && local.second === second && candidate % 1000 === millisecond) matches.push(candidate);
    }
    if (!matches.length) throw new ProductError("validation.62ed9c052a3f");
    // A repeated fall-back clock time maps to two instants; choose its earlier
    // occurrence deterministically and retain the explicit timezone in the query.
    return BigInt(Math.min(...matches));
  } catch (error) {
    if (error instanceof ProductError) throw error;
    throw new ProductError("validation.24d222761f98");
  }
}
