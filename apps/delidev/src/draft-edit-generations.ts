// SPDX-License-Identifier: Apache-2.0
/** Connection-owned edit history, separate from disposable empty draft payloads.
 * Bounded counters never wrap or forget an active owner's edit-and-undo history. */
export class DraftEditGenerations {
  private values = new Map<string, bigint>();
  read = (id: string) => this.values.get(id) ?? 0n;
  advance(id: string): boolean {
    const previous = this.read(id);
    if (
      previous === (1n << 64n) - 1n ||
      (!this.values.has(id) && this.values.size >= 1000)
    )
      return false;
    this.values.set(id, previous + 1n);
    return true;
  }
}
