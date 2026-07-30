const microsPerDollar = 1_000_000n;
const microsPerCent = 10_000n;
const wholeDollarFormatter = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 0,
});

export function formatUsdMicros(value = 0n): string {
  const negative = value < 0n;
  const magnitude = negative ? -value : value;
  const maximumFractionDigits = magnitude < microsPerCent ? 6 : 2;
  const minimumFractionDigits = magnitude < microsPerCent ? 4 : 2;
  const roundingUnit =
    10n ** BigInt(6 - maximumFractionDigits);
  let rounded = magnitude / roundingUnit;
  if ((magnitude % roundingUnit) * 2n >= roundingUnit) {
    rounded += 1n;
  }
  const fractionalScale = microsPerDollar / roundingUnit;
  const dollars = rounded / fractionalScale;
  let fractional = (rounded % fractionalScale)
    .toString()
    .padStart(maximumFractionDigits, "0");
  while (
    fractional.length > minimumFractionDigits &&
    fractional.endsWith("0")
  ) {
    fractional = fractional.slice(0, -1);
  }
  const sign = negative && rounded !== 0n ? "-" : "";
  return `${sign}$${wholeDollarFormatter.format(dollars)}.${fractional}`;
}

export function formatEnumLabel(value: string | number): string {
  return String(value)
    .replaceAll("_", " ")
    .toLowerCase()
    .replace(/^\w/, (character) => character.toUpperCase());
}

export function createIdempotencyKey(): { key: string } {
  return { key: crypto.randomUUID() };
}

export function createUuidV7(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  let timestamp = BigInt(Date.now());
  for (let index = 5; index >= 0; index -= 1) {
    bytes[index] = Number(timestamp & 0xffn);
    timestamp >>= 8n;
  }
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const value = [...bytes]
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
  return [
    value.slice(0, 8),
    value.slice(8, 12),
    value.slice(12, 16),
    value.slice(16, 20),
    value.slice(20),
  ].join("-");
}
