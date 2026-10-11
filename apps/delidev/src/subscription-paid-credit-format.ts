// SPDX-License-Identifier: Apache-2.0
/** Format validated decimal evidence without losing any digits to floating point. */
export function formatPaidCreditBalance(balance: string, locale: string): string {
  const [whole, fraction = ""] = balance.split(".");
  const integer = BigInt(whole);
  const cents = (fraction + "00").slice(0, 2);
  const tiny = integer === 0n && cents === "00" && /[1-9]/.test(fraction);
  let rounded = integer * 100n + BigInt(cents);
  if ((fraction[2] ?? "0") >= "5") rounded += 1n;
  const decimal = new Intl.NumberFormat(locale).formatToParts(1.1).find(part => part.type === "decimal")!.value;
  if (tiny) return `<0${decimal}01`;
  return `${new Intl.NumberFormat(locale).format(rounded / 100n)}${decimal}${String(rounded % 100n).padStart(2, "0")}`;
}
