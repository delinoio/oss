// SPDX-License-Identifier: Apache-2.0
// Every module that renders a listbox, menu, combobox, popover or details must
// be registered with the treatment that keeps it layout-invariant, so a new
// in-flow dropdown cannot land without an explicit decision. The desktop
// contract carries the same inventory as a table.
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { describe, expect, it } from "vitest";
import { dropdownInventory, DropdownTreatment } from "./dropdown-inventory";

const source = join(process.cwd(), "src");
const contract = readFileSync(
  join(process.cwd(), "../../docs/apps-delidev-desktop-contract.md"),
  "utf8",
);
// Markers of a dropdown-like surface. role="..." inside a querySelector string
// (preceded by "[") is a lookup, not a surface, and aria-haspopup="dialog" only
// names a modal opener.
const markers = [
  /\bpopover=/,
  /<details\b/,
  /(?<!\[)role="(listbox|menu|combobox)"/,
  /aria-haspopup="(menu|listbox)"/,
];
// Nested application modules (for example the QA renderer) are scanned too, so
// a dropdown-like surface cannot bypass registration by living in a subfolder.
const sourceFiles = (directory: string): string[] =>
  readdirSync(directory, { withFileTypes: true }).flatMap((entry) =>
    entry.isDirectory()
      ? sourceFiles(join(directory, entry.name))
      : [join(directory, entry.name)],
  );
const marked = sourceFiles(source)
  .map((path) => relative(source, path).split(sep).join("/"))
  .filter((file) => file.endsWith(".tsx") && !/\.(test|fixture)\./.test(file))
  .filter((file) => {
    const text = readFileSync(join(source, file), "utf8");
    return markers.some((marker) => marker.test(text));
  })
  .sort();

describe("dropdown inventory", () => {
  it("registers every dropdown-like module exactly once", () => {
    const registered = dropdownInventory.map((entry) => entry.file);
    expect(new Set(registered).size).toBe(registered.length);
    expect([...registered].sort()).toEqual(marked);
  });

  it("keeps each treatment backed by its source", () => {
    for (const entry of dropdownInventory) {
      const text = readFileSync(join(source, entry.file), "utf8");
      const proofs: Record<DropdownTreatment, boolean> = {
        [DropdownTreatment.AnchoredPopup]:
          /useAnchoredPopup\(/.test(text) && /popover="manual"/.test(text),
        [DropdownTreatment.ScrollPicker]: /pickerOverlayGeometry/.test(text),
        [DropdownTreatment.TopLayerPopover]: /\bpopover=/.test(text),
        [DropdownTreatment.ModalDialog]: /showModal\(/.test(text),
        [DropdownTreatment.ReservedRegion]: /role="listbox"/.test(text),
        [DropdownTreatment.PersistentDisclosure]: /<details\b/.test(text),
        [DropdownTreatment.Exception]: /<details\b/.test(text),
      };
      expect(proofs[entry.treatment], entry.file).toBe(true);
    }
  });

  it("limits in-flow exceptions to documented reasons", () => {
    const exceptions = dropdownInventory.filter(
      (entry) => entry.treatment === DropdownTreatment.Exception,
    );
    expect(exceptions.map((entry) => entry.file)).toEqual([
      "session-browser.tsx",
    ]);
    for (const entry of exceptions)
      expect(entry.evidence.length).toBeGreaterThan(40);
  });

  it("lists every registered module and its geometry evidence in the desktop contract", () => {
    // The table, not the surrounding narrative, is the contract: parse its rows
    // and compare each exact file/family/treatment/evidence tuple.
    const rows = inventoryTable();
    expect(rows.map((row) => row.file)).toEqual(
      dropdownInventory.map((entry) => entry.file),
    );
    for (const entry of dropdownInventory) {
      const row = rows.find((candidate) => candidate.file === entry.file);
      expect(row, entry.file).toBeDefined();
      expect(row, entry.file).toEqual({
        file: entry.file,
        family: entry.family,
        treatment: treatmentLabels[entry.treatment],
        evidence: plain(entry.evidence),
      });
    }
    for (const script of new Set(
      dropdownInventory.flatMap(
        (entry) => entry.evidence.match(/scripts\/[\w-]+\.mjs/g) ?? [],
      ),
    )) {
      expect(existsSync(join(process.cwd(), script)), script).toBe(true);
      expect(contract, script).toContain(script);
    }
  });

  it("detects a missing, renamed or altered inventory row", () => {
    const table = inventoryTableText();
    const [first] = dropdownInventory;
    const mutations = [
      table.replace(new RegExp(`^\\| \`${first.file}\`.*\\n`, "m"), ""),
      table.replace(first.family, `${first.family} (changed)`),
      table.replace("Anchored popup", "Top-layer popover"),
      table.replace(
        "scripts/test-reasoning-effort-layout.mjs",
        "scripts/x.mjs",
      ),
    ];
    for (const mutated of mutations) {
      expect(mutated).not.toBe(table);
      expect(parseInventoryRows(mutated)).not.toEqual(inventoryTable());
    }
  });
});

const treatmentLabels: Record<DropdownTreatment, string> = {
  [DropdownTreatment.AnchoredPopup]: "Anchored popup",
  [DropdownTreatment.ScrollPicker]: "ScrollPicker",
  [DropdownTreatment.TopLayerPopover]: "Top-layer popover",
  [DropdownTreatment.ModalDialog]: "Modal dialog body",
  [DropdownTreatment.ReservedRegion]: "Reserved region",
  [DropdownTreatment.PersistentDisclosure]: "Persistent disclosure",
  [DropdownTreatment.Exception]: "Exception",
};
// Markdown backticks only format table cells; compare the plain text.
const plain = (value: string) => value.replaceAll("`", "").trim();
function inventoryTableText(): string {
  const start = contract.indexOf("\n### Dropdown inventory\n");
  expect(
    start,
    "the contract has a Dropdown inventory section",
  ).toBeGreaterThan(-1);
  const rest = contract.slice(start + 1);
  const end = rest.search(/\n### /);
  return end < 0 ? rest : rest.slice(0, end);
}
function parseInventoryRows(text: string) {
  return text
    .split("\n")
    .filter((line) => /^\| `/.test(line))
    .map((line) => {
      const cells = line
        .replace(/^\||\|$/g, "")
        .split(/(?<!\\)\|/)
        .map((cell) => plain(cell));
      return {
        file: cells[0],
        family: cells[1],
        treatment: cells[2],
        evidence: cells[3],
      };
    });
}
const inventoryTable = () => parseInventoryRows(inventoryTableText());
