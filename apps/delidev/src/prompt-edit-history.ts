// SPDX-License-Identifier: Apache-2.0

/** Kinds of edits that the composer history can coalesce into one step. */
export enum PromptEditKind {
  Typing = "typing",
  Deleting = "deleting",
  Composition = "composition",
  /** Never coalesced: paste, cut, conversion, completion and replacement. */
  Discrete = "discrete",
}

export interface PromptSelection {
  start: number;
  end: number;
  direction: "forward" | "backward" | "none";
}

export interface PromptSnapshot<Binding> {
  value: string;
  bindings: Binding[];
  selection: PromptSelection;
}

const MAX_ENTRIES = 200;

/**
 * Adapter-owned undo history for a composer textarea. Programmatic value
 * changes (Markdown link conversion) clear the native undo stack, so every
 * edit is recorded here and undo/redo are applied through the owner instead.
 * A conversion and the paste or delimiter that triggered it are one snapshot.
 */
export class PromptEditHistory<Binding> {
  private entries: PromptSnapshot<Binding>[] = [];
  private index = -1;
  private lastKind = PromptEditKind.Discrete;

  /** Drops stale history when the draft changed outside this adapter. */
  private align(current: PromptSnapshot<Binding>): void {
    if (this.entries[this.index]?.value === current.value) return;
    this.entries = [current];
    this.index = 0;
    this.lastKind = PromptEditKind.Discrete;
  }

  reset(): void {
    this.entries = [];
    this.index = -1;
    this.lastKind = PromptEditKind.Discrete;
  }

  record(
    before: PromptSnapshot<Binding>,
    after: PromptSnapshot<Binding>,
    kind: PromptEditKind,
    coalesce: boolean,
  ): void {
    this.align(before);
    this.entries.length = this.index + 1;
    if (
      coalesce &&
      kind !== PromptEditKind.Discrete &&
      kind === this.lastKind &&
      this.index > 0
    )
      this.entries[this.index] = after;
    else {
      this.entries.push(after);
      this.index += 1;
    }
    this.lastKind = kind;
    if (this.entries.length > MAX_ENTRIES) {
      this.entries.shift();
      this.index -= 1;
    }
  }

  undo(current: PromptSnapshot<Binding>): PromptSnapshot<Binding> | undefined {
    this.align(current);
    if (this.index <= 0) return;
    this.index -= 1;
    this.lastKind = PromptEditKind.Discrete;
    return this.entries[this.index];
  }

  redo(current: PromptSnapshot<Binding>): PromptSnapshot<Binding> | undefined {
    this.align(current);
    if (this.index >= this.entries.length - 1) return;
    this.index += 1;
    this.lastKind = PromptEditKind.Discrete;
    return this.entries[this.index];
  }

  /** Re-aligns after an undo or redo the owner rejected. */
  revert(direction: "undo" | "redo"): void {
    this.index += direction === "undo" ? 1 : -1;
  }
}
