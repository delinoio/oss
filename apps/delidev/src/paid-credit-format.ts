// SPDX-License-Identifier: Apache-2.0
/** Exact nonnegative source decimals remain owned by the observation. */
export function formatPaidCreditBalance(balance: string, locale: string): string | undefined {
 if (balance.length > 64 || !/^[0-9]+(?:\.[0-9]+)?$/.test(balance)) return undefined;
 const [whole, fraction = ""] = balance.split(".");
 const integer = BigInt(whole), digits = fraction.padEnd(3, "0");
 const format = new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
 const separator = format.formatToParts(0n).find(part => part.type === "decimal")!.value;
 if (integer === 0n && digits.slice(0, 2) === "00" && /[1-9]/.test(fraction)) return `<0${separator}01`;
 const cents = integer * 100n + BigInt(digits.slice(0, 2)) + (digits[2] >= "5" ? 1n : 0n);
 const grouped = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(cents / 100n);
 return `${grouped}${separator}${String(cents % 100n).padStart(2, "0")}`;
}
