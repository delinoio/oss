const serializationFallback = "{\n  \"error\": \"failed to serialize value\"\n}";

export function stringifyForUi(value: unknown): string {
  try {
    return JSON.stringify(
      value,
      (_key, nestedValue) => (typeof nestedValue === "bigint" ? nestedValue.toString() : nestedValue),
      2,
    );
  } catch {
    return serializationFallback;
  }
}
