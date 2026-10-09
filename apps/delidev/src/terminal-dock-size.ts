// SPDX-License-Identifier: Apache-2.0
export function terminalDockHeight(width: number, bodyHeight: number, maximized: boolean, requested?: number) {
  const compact = width < 900 || bodyHeight < 600;
  if (maximized || compact && requested === undefined) return Math.max(0, bodyHeight);
  return Math.min(Math.max(0, bodyHeight * .7), Math.max(Math.min(200, bodyHeight * .7), requested ?? bodyHeight * .4));
}
