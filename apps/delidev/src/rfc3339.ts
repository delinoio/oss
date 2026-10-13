// SPDX-License-Identifier: Apache-2.0
const RFC3339_PARTS =
  /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/;

/** Validate the source wall time before converting its instant. Date.parse
 * alone normalizes impossible calendar dates (February 30, hour 24, second 60)
 * into valid instants and cannot establish valid RFC3339 evidence. This module
 * stays pure so protocol validators can use it without localization. */
export function rfc3339Instant(value: string): number | undefined {
  const match = RFC3339_PARTS.exec(value);
  if (!match) return;
  const wall = new Date(`${match[1]}T${match[2]}Z`);
  if (
    !Number.isFinite(wall.getTime()) ||
    wall.toISOString().slice(0, 19) !== `${match[1]}T${match[2]}`
  )
    return;
  if (
    match[4] !== "Z" &&
    (Number(match[4]!.slice(1, 3)) > 23 || Number(match[4]!.slice(4)) > 59)
  )
    return;
  const instant = Date.parse(value);
  return Number.isFinite(instant) ? instant : undefined;
}
