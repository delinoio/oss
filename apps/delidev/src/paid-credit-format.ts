// SPDX-License-Identifier: Apache-2.0
import { formatDecimal } from "./localization";

/** Round only the display. Keep the source string for exact-value disclosure. */
export function formatPaidCreditBalance(balance: string): string | null {
  if (balance.length > 64 || !/^[0-9]+(?:\.[0-9]+)?$/.test(balance))
    return null;
  const [whole, fraction = ""] = balance.split(".");
  const integer = whole.replace(/^0+(?=\d)/, "");
  if (/^0+$/.test(integer) && !/[1-9]/.test(fraction))
    return formatDecimal("0.00");
  if (integer === "0" && !/[1-9]/.test(fraction.slice(0, 2)))
    return `<${formatDecimal("0.01")}`;
  let cents = integer + fraction.padEnd(2, "0").slice(0, 2);
  // Carry through decimal digits without a floating-point conversion.
  if ((fraction[2] ?? "0") >= "5") {
    const digits = cents.split("");
    let index = digits.length - 1;
    while (index >= 0 && digits[index] === "9") digits[index--] = "0";
    if (index < 0) digits.unshift("1");
    else digits[index] = String.fromCharCode(digits[index].charCodeAt(0) + 1);
    cents = digits.join("");
  }
  return formatDecimal(`${cents.slice(0, -2)}.${cents.slice(-2)}`);
}
