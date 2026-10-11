// SPDX-License-Identifier: Apache-2.0
/** Format a validated balance without converting its decimal evidence to Number. */
export function formatPaidCreditBalance(value: string, locale: string): string {
  if (value.length > 64 || !/^[0-9]+(?:\.[0-9]+)?$/.test(value)) throw new Error("Invalid paid-credit balance");
  const [integer, fraction = ""] = value.split(".");
  const whole = BigInt(integer);
  if (whole === 0n && /[1-9]/.test(fraction) && !/[1-9]/.test(fraction.slice(0, 2))) return "<0.01";
  const cents = whole * 100n + BigInt(fraction.slice(0, 2).padEnd(2, "0")) + (fraction[2] >= "5" ? 1n : 0n);
  const formatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 });
  const separator = new Intl.NumberFormat(locale).formatToParts(1.1).find(part => part.type === "decimal")?.value ?? ".";
  return `${formatter.format(cents / 100n)}${separator}${String(cents % 100n).padStart(2, "0")}`;
}
