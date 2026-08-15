export const DEVHUD_CORRELATION_ID_HEADER = "x-devhud-correlation-id" as const;

const UUID_V7_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

/** Returns a canonical UUID v7 correlation ID or undefined for invalid input. */
export function getDevHudCorrelationId(headers: Headers): string | undefined {
  const value = headers.get(DEVHUD_CORRELATION_ID_HEADER)?.toLowerCase();
  return value !== undefined && UUID_V7_PATTERN.test(value) ? value : undefined;
}
