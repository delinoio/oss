// Keep nanoseconds when comparing Go RFC3339Nano timestamps. Date.parse alone
// rounds them to milliseconds and also normalizes invalid calendar dates.
export function utcTimestamp(value: unknown): string | undefined {
  if (typeof value !== "string") return;
  const match = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z$/.exec(value);
  if (!match) return;
  const whole = `${match[1]}Z`, parsed = Date.parse(whole);
  if (!Number.isFinite(parsed) || new Date(parsed).toISOString() !== `${match[1]}.000Z`) return;
  return `${match[1]}.${(match[2] ?? "").padEnd(9, "0")}Z`;
}
