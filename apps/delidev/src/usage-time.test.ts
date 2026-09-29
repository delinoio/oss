import { expect, it } from "vitest";
import { detectDeviceTimeZone, localDateTimeToUnixMs } from "./usage-time";

it("converts wall-clock input through its explicit IANA timezone", () => {
  expect(localDateTimeToUnixMs("2026-09-01T12:00", "Asia/Seoul")).toBe(BigInt(Date.UTC(2026, 8, 1, 3)));
  expect(localDateTimeToUnixMs("2026-09-01T12:00", "UTC")).toBe(BigInt(Date.UTC(2026, 8, 1, 12)));
});

it("uses the earlier instant for repeated fall-back times and rejects missing spring times", () => {
  expect(localDateTimeToUnixMs("2026-11-01T01:30", "America/New_York")).toBe(BigInt(Date.UTC(2026, 10, 1, 5, 30)));
  expect(() => localDateTimeToUnixMs("2026-03-08T02:30", "America/New_York")).toThrow(/does not exist/);
});

it("rejects invalid dates and has an explicit UTC device-zone fallback", () => {
  expect(() => localDateTimeToUnixMs("2026-02-30T12:00", "UTC")).toThrow(/valid calendar/);
  expect(detectDeviceTimeZone().length).toBeGreaterThan(0);
});
