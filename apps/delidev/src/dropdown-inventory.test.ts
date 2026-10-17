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
    for (const entry of dropdownInventory) {
      expect(contract, entry.file).toContain(`\`${entry.file}\``);
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
});
