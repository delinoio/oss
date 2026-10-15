// SPDX-License-Identifier: Apache-2.0
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";
import chordFixtures from "./shortcut-chord-fixtures.json";
import enShortcuts from "./locales/en/shortcuts.json";
import koShortcuts from "./locales/ko/shortcuts.json";
import {
  editableShortcutCatalog,
  fixedNativeShortcutCatalog,
  validShortcutChord,
} from "./shortcut-preferences";
import { globalShortcutBindings, ShortcutId } from "./shortcuts";

// The desktop contract owns the shortcut chord table. The renderer validator,
// the shared fixture, the localized fixed-shortcut help and the native
// validator (see shortcut_preferences.rs) must all match that table.
const docsDirectory = resolve(process.cwd(), "../../docs");
const contract = readFileSync(
  resolve(docsDirectory, "apps-delidev-desktop-contract.md"),
  "utf8",
);

function tableCells(label: string): string[] {
  const rows = contract
    .split("\n")
    .filter((line) => line.startsWith("|"))
    .map((line) => line.split("|").map((cell) => cell.trim()))
    .filter((cells) => cells[1] === label);
  expect(rows, label).toHaveLength(1);
  return rows[0]!;
}

function tableRow(label: string): string[] {
  const cell = tableCells(label)[2]!;
  return [...cell.matchAll(/`([^`]+)`/g)].map((match) => match[1]!);
}

const reservedPlain = new Set(tableRow("Reserved unshifted primary keys"));
const reservedShift = new Set(tableRow("Reserved Shift primary keys"));
const helpKeys = tableRow("Keys named in the fixed shortcut help");
const newSessionDefault = tableRow("Editable New session default");
const everyKey = [..."abcdefghijklmnopqrstuvwxyz0123456789"];

it("parses a complete shortcut chord table from the desktop contract", () => {
  expect(reservedPlain.size).toBeGreaterThan(0);
  expect(reservedShift.size).toBeGreaterThan(0);
  expect(helpKeys.length).toBeGreaterThan(0);
  expect(newSessionDefault).toEqual(["N"]);
  expect(reservedPlain.has("n")).toBe(false);
  expect(helpKeys).not.toContain("N");
  expect(tableCells("Native New Window accelerator")[2]).toBe("none");
});

it("keeps the renderer validator equal to the contract reservation table", () => {
  for (const key of everyKey) {
    expect(validShortcutChord({ key, shift: false }), key).toBe(
      !reservedPlain.has(key),
    );
    expect(validShortcutChord({ key, shift: true }), `shift+${key}`).toBe(
      !reservedShift.has(key),
    );
  }
  expect(validShortcutChord({ key: "Enter", shift: false })).toBe(true);
  expect(validShortcutChord({ key: "Enter", shift: true })).toBe(true);
});

it("keeps fixed native entries and the shared fixture inside the table", () => {
  for (const action of fixedNativeShortcutCatalog)
    expect(reservedPlain.has(action.key), action.key).toBe(true);
  for (const { key, shift, valid } of chordFixtures.chords) {
    if (!/^[a-z0-9]$/.test(key)) continue;
    expect(valid, `${key}/${shift}`).toBe(
      !(shift ? reservedShift : reservedPlain).has(key),
    );
  }
});

it("keeps the New session default equal to the contract table", () => {
  const expected = newSessionDefault[0]!.toLowerCase();
  expect(globalShortcutBindings[ShortcutId.NewSession]).toEqual([
    { key: expected, primary: true },
  ]);
  expect(
    editableShortcutCatalog.find(
      (action) => action.id === ShortcutId.NewSession,
    )?.defaults,
  ).toEqual([{ key: expected, primary: true }]);
});

function helpKeysFrom(text: string): string[] {
  const list = text.slice(text.indexOf("Control + ") + "Control + ".length);
  return [...list.matchAll(/(?<![A-Za-z])[A-Z](?![A-Za-z])|\d–\d/g)].map(
    (match) => match[0],
  );
}

it("lists exactly the contract help keys in English and Korean fixed help", () => {
  for (const text of [
    enShortcuts["shortcut-settings.fixedHelp"],
    koShortcuts["shortcut-settings.fixedHelp"],
  ]) {
    expect(helpKeysFrom(text)).toEqual(helpKeys);
    for (const key of helpKeys) {
      if (/^[A-Z]$/.test(key))
        expect(reservedPlain.has(key.toLowerCase())).toBe(true);
    }
  }
  const range = helpKeys.find((key) => key.includes("–"));
  expect(range).toBe("1–9");
  for (const digit of "123456789") expect(reservedPlain.has(digit)).toBe(true);
});

it("leaves no document naming N as reserved or Shift+N as the New session default", () => {
  const stale = [
    /K\/N\/W/,
    /K, N, W/,
    /primary\+Shift\+N for New session/,
    /N New Window chord/,
    /New Window (?:uses|binds) `?CmdOrCtrl\+N/,
    /primary\+N \(native New Window\)/,
  ];
  for (const name of readdirSync(docsDirectory)) {
    if (!name.endsWith(".md")) continue;
    const text = readFileSync(resolve(docsDirectory, name), "utf8");
    for (const pattern of stale)
      expect(pattern.test(text), `${name} ${pattern}`).toBe(false);
  }
});
