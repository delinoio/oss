// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  EditHistory,
  EditKind,
  MAX_HISTORY_ENTRIES,
  type HistorySnapshot,
} from "./prompt-edit-history";

const snap = (text: string, caret = text.length): HistorySnapshot<string> => ({
  text,
  bindings: [],
  selection: { start: caret, end: caret, direction: "none" },
});

it("extends typing that continues from the exact previous result", () => {
  const history = new EditHistory<string>();
  history.record(EditKind.Typing, snap(""), snap("a"));
  history.record(EditKind.Typing, snap("a"), snap("ab"));
  expect(history.size).toBe(1);
  expect(history.peekUndo()?.text).toBe("");
  // A caret move away from the previous result starts a new entry.
  history.record(EditKind.Typing, snap("ab", 0), snap("xab", 1));
  expect(history.size).toBe(2);
});

it("seals at a boundary and never merges other kinds", () => {
  const history = new EditHistory<string>();
  history.record(EditKind.Typing, snap(""), snap("a "), { seal: true });
  history.record(EditKind.Typing, snap("a "), snap("a b"));
  history.record(EditKind.Paste, snap("a b"), snap("a bc"));
  history.record(EditKind.Paste, snap("a bc"), snap("a bcd"));
  expect(history.size).toBe(4);
});

it("keeps one composition session as one entry and amends its result", () => {
  const history = new EditHistory<string>();
  history.record(EditKind.Composition, snap(""), snap("ㅎ"), { session: 1 });
  history.record(EditKind.Composition, snap("ㅎ"), snap("한"), { session: 1 });
  expect(history.size).toBe(1);
  expect(history.amend(1, snap("한 "))).toBe(true);
  expect(history.amend(2, snap("x"))).toBe(false);
  history.commitUndo();
  expect(history.peekRedo()?.text).toBe("한 ");
});

it("clears redo on a new edit and bounds retained entries", () => {
  const history = new EditHistory<string>();
  history.record(EditKind.Paste, snap(""), snap("a"));
  history.commitUndo();
  expect(history.canRedo).toBe(true);
  history.record(EditKind.Paste, snap(""), snap("b"));
  expect(history.canRedo).toBe(false);
  for (let index = 0; index < MAX_HISTORY_ENTRIES + 20; index++)
    history.record(EditKind.Paste, snap(`${index}`), snap(`${index + 1}`));
  expect(history.size).toBe(MAX_HISTORY_ENTRIES);
  history.reset();
  expect(history.size).toBe(0);
});

it("does not let the entry exposed by undo absorb the next typing", () => {
  const history = new EditHistory<string>();
  history.record(EditKind.Typing, snap(""), snap("a"));
  history.record(EditKind.Paste, snap("a"), snap("aX"), { seal: true });
  history.commitUndo();
  // The restored caret equals the end of the exposed typing entry.
  history.record(EditKind.Typing, snap("a"), snap("ab"));
  expect(history.size).toBe(2);
  expect(history.peekUndo()?.text).toBe("a");
  history.commitUndo();
  expect(history.peekUndo()?.text).toBe("");
});

it("seals the newest entry and ignores an empty history", () => {
  const history = new EditHistory<string>();
  history.seal();
  history.record(EditKind.Typing, snap(""), snap("a"));
  history.seal();
  history.record(EditKind.Typing, snap("a"), snap("ab"));
  expect(history.size).toBe(2);
});
