// SPDX-License-Identifier: Apache-2.0
export const TERMINAL_INPUT_LIMIT = 65536;
export const TERMINAL_INPUT_CHUNK = 32768;
export type TerminalDimensions = { rows: number; columns: number };

/** Contains only unsent bytes. The connection mutation registry separately owns
 * the dispatched exact request through acknowledgment or explicit recovery. */
export class TerminalInputQueue {
  private bytes = new Uint8Array();
  private measured?: TerminalDimensions;
  private accepted?: TerminalDimensions;
  get size() { return this.bytes.byteLength; }
  enqueue(bytes: Uint8Array): boolean {
    if (this.bytes.byteLength + bytes.byteLength > TERMINAL_INPUT_LIMIT) return false;
    const next = new Uint8Array(this.bytes.byteLength + bytes.byteLength);
    next.set(this.bytes); next.set(bytes, this.bytes.byteLength); this.bytes = next; return true;
  }
  resize(rows: number, columns: number) {
    if (!Number.isInteger(rows) || !Number.isInteger(columns) || rows < 1 || rows > 500 || columns < 1 || columns > 1000) return;
    this.measured = { rows, columns };
  }
  next(): { input: Uint8Array } | TerminalDimensions | undefined {
    if (this.bytes.length) { const input = this.bytes.slice(0, TERMINAL_INPUT_CHUNK); this.bytes = this.bytes.slice(input.length); return { input }; }
    if (this.measured && (this.measured.rows !== this.accepted?.rows || this.measured.columns !== this.accepted?.columns)) return { ...this.measured };
    return undefined;
  }
  acknowledge(dimensions?: TerminalDimensions) { if (dimensions) this.accepted = dimensions; }
  discard() { const count = this.bytes.byteLength; this.bytes = new Uint8Array(); this.measured = undefined; return count; }
}
export function binaryInput(value: string): Uint8Array { return Uint8Array.from(value, character => character.charCodeAt(0) & 255); }
