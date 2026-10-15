// SPDX-License-Identifier: Apache-2.0
// Adapter-owned undo and redo for canonical composer drafts. The textarea shows
// a projection that changes when links reveal or collapse, and programmatic
// value writes discard native undo history, so every canonical edit is recorded
// here as one transaction instead. Entries never log or expose prompt text.

export interface HistorySelection {
  start: number;
  end: number;
  direction: "forward" | "backward" | "none";
}

export interface HistorySnapshot<Binding> {
  text: string;
  bindings: readonly Binding[];
  selection: HistorySelection;
}

export enum EditKind {
  Typing = "typing",
  Deletion = "deletion",
  Paste = "paste",
  Cut = "cut",
  Completion = "completion",
  Conversion = "conversion",
  Composition = "composition",
}

interface HistoryEntry<Binding> {
  kind: EditKind;
  before: HistorySnapshot<Binding>;
  after: HistorySnapshot<Binding>;
  /** A sealed entry never absorbs later edits (a word or session boundary). */
  sealed: boolean;
  /** Identifies one IME composition so all its updates stay one entry. */
  session?: number;
}

/** Bounds retained undo state: entry count and total retained text units. */
export const MAX_HISTORY_ENTRIES = 100;
export const MAX_HISTORY_TEXT_UNITS = 2_000_000;

export class EditHistory<Binding> {
  private undoStack: HistoryEntry<Binding>[] = [];
  private redoStack: HistoryEntry<Binding>[] = [];

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }
  get canRedo(): boolean {
    return this.redoStack.length > 0;
  }
  get size(): number {
    return this.undoStack.length + this.redoStack.length;
  }

  reset() {
    this.undoStack = [];
    this.redoStack = [];
  }

  /**
   * Records one accepted edit. Consecutive typing or deletion that continues
   * from the previous entry's exact result extends that entry; a boundary
   * (`seal`), a caret move, a different kind or a conversion starts a new one.
   * Recording clears the redo stack.
   */
  record(
    kind: EditKind,
    before: HistorySnapshot<Binding>,
    after: HistorySnapshot<Binding>,
    options: { seal?: boolean; session?: number } = {},
  ) {
    this.redoStack = [];
    const top = this.undoStack[this.undoStack.length - 1];
    const continues =
      top !== undefined &&
      !top.sealed &&
      top.kind === kind &&
      (kind === EditKind.Typing ||
        kind === EditKind.Deletion ||
        (kind === EditKind.Composition &&
          options.session !== undefined &&
          top.session === options.session)) &&
      top.after.text === before.text &&
      top.after.selection.start === before.selection.start &&
      top.after.selection.end === before.selection.end;
    if (top && continues) {
      top.after = after;
      top.sealed = options.seal ?? false;
    } else {
      this.undoStack.push({
        kind,
        before,
        after,
        sealed: options.seal ?? false,
        session: options.session,
      });
    }
    this.trim();
  }

  /** Replaces the result of the newest entry of one composition session. */
  amend(session: number, after: HistorySnapshot<Binding>): boolean {
    const top = this.undoStack[this.undoStack.length - 1];
    if (!top || top.session !== session) return false;
    top.after = after;
    top.sealed = true;
    return true;
  }

  /** Seals the newest entry so the next edit starts a separate transaction. */
  seal() {
    const top = this.undoStack[this.undoStack.length - 1];
    if (top) top.sealed = true;
  }

  /** Returns the snapshot to restore without moving it; commit with `commitUndo`. */
  peekUndo(): HistorySnapshot<Binding> | undefined {
    return this.undoStack[this.undoStack.length - 1]?.before;
  }
  commitUndo() {
    const entry = this.undoStack.pop();
    if (!entry) return;
    entry.sealed = true;
    this.redoStack.push(entry);
    // Undo is a boundary: the entry now on top must not absorb the next edit,
    // even though the restored selection equals the end of its result.
    this.seal();
  }
  peekRedo(): HistorySnapshot<Binding> | undefined {
    return this.redoStack[this.redoStack.length - 1]?.after;
  }
  commitRedo() {
    const entry = this.redoStack.pop();
    if (!entry) return;
    entry.sealed = true;
    this.undoStack.push(entry);
  }

  private trim() {
    const units = () =>
      this.undoStack.reduce(
        (total, entry) =>
          total + entry.before.text.length + entry.after.text.length,
        0,
      );
    while (
      this.undoStack.length > MAX_HISTORY_ENTRIES ||
      (this.undoStack.length > 1 && units() > MAX_HISTORY_TEXT_UNITS)
    )
      this.undoStack.shift();
  }
}
