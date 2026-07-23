export function formatUsdMicros(value = 0n): string {
  const dollars = Number(value) / 1_000_000;
  if (!Number.isSafeInteger(Number(value))) {
    return "Price unavailable";
  }
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: dollars < 0.01 ? 4 : 2,
    maximumFractionDigits: dollars < 0.01 ? 6 : 2,
  }).format(dollars);
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
