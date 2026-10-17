// SPDX-License-Identifier: Apache-2.0
import { readdirSync, readFileSync } from "node:fs";
import { expect, it } from "vitest";

/**
 * Enforces the owner inventory in the desktop contract section "Desktop form
 * submission and Enter". Every renderer module that owns a form or an editable
 * text control must be classified there, and every form owner must wire the
 * shared Enter guard and claim only its own submit event.
 */
enum OwnerStatus {
  /** Added or reworked to the shared form-owner contract. */
  Converted = "converted",
  /** Already owned a form; now wired to the shared guard and submit claim. */
  NativeForm = "native-form",
  /** Message composer whose Enter semantics belong to its send contract. */
  Composer = "composer",
  /** No single-line text input of its own, or the containing owner supplies the form. */
  NotApplicable = "not-applicable",
}
interface Row {
  patterns: string[];
  status: OwnerStatus;
}

const CONTRACT = "../../docs/apps-delidev-desktop-contract.md";
/** Forms inside a composer module that intentionally keep their own Enter send contract. */
const COMPOSER_FORMS: Record<string, number> = {
  "new-session.tsx": 1,
  "session.tsx": 1,
  "session-fork.tsx": 1,
};

function sources() {
  return readdirSync("src")
    .filter(
      (name) =>
        name.endsWith(".tsx") &&
        !name.includes(".test.") &&
        !name.includes(".fixture."),
    )
    .sort();
}
function inventory(): Row[] {
  const text = readFileSync(CONTRACT, "utf8");
  const start = text.indexOf("#### Owner inventory");
  expect(start, "Owner inventory heading").toBeGreaterThan(-1);
  const rest = text.slice(start + 1);
  const end = rest.search(/\n#{1,4} /);
  const section = rest.slice(0, end < 0 ? undefined : end);
  const rows: Row[] = [];
  for (const line of section.split("\n")) {
    if (!line.startsWith("|") || /^\|\s*-/.test(line)) continue;
    const cells = line
      .split("|")
      .slice(1, -1)
      .map((cell) => cell.trim());
    if (cells[0]?.startsWith("Owner")) continue;
    const patterns = [...cells[0]!.matchAll(/`([a-z0-9*-]+\.tsx)`/g)].map(
      (match) => match[1]!,
    );
    const status = /`([a-z-]+)`/.exec(cells[2] ?? "")?.[1];
    expect(patterns.length, line).toBeGreaterThan(0);
    expect(Object.values(OwnerStatus), line).toContain(status);
    rows.push({ patterns, status: status as OwnerStatus });
  }
  return rows;
}
function matches(pattern: string, name: string) {
  return new RegExp(
    `^${pattern.replace(/[.]/g, "\\.").replace(/\*/g, "[a-z0-9-]*")}$`,
  ).test(name);
}
function statusOf(rows: Row[], name: string) {
  const found = rows.filter((row) =>
    row.patterns.some((pattern) => matches(pattern, name)),
  );
  expect(found.length, `${name} must appear in exactly one inventory row`).toBe(
    1,
  );
  return found[0]!.status;
}
const count = (text: string, pattern: RegExp) =>
  [...text.matchAll(pattern)].length;

it("classifies every renderer module that owns a form or editable text control", () => {
  const rows = inventory();
  const ownerPattern = /<form[\s>]|<input[\s>]|<textarea[\s>]|<TextField[\s>]/;
  for (const name of sources()) {
    const owns = ownerPattern.test(readFileSync(`src/${name}`, "utf8"));
    const listed = rows.filter((row) =>
      row.patterns.some((pattern) => matches(pattern, name)),
    );
    if (owns)
      expect(listed.length, `${name} owns a form or text control`).toBe(1);
    else expect(listed.length, `${name} no longer owns one`).toBeLessThan(2);
  }
});

it("lists only modules that exist and keeps the status vocabulary closed", () => {
  const names = sources();
  for (const row of inventory())
    for (const pattern of row.patterns)
      expect(
        names.some((name) => matches(pattern, name)),
        `${pattern} must match a renderer module`,
      ).toBe(true);
});

it("wires the shared Enter guard and own-submit claim into every form owner", () => {
  const rows = inventory();
  for (const name of sources()) {
    const text = readFileSync(`src/${name}`, "utf8");
    const forms = count(text, /<form[\s>]/g);
    const status = forms ? statusOf(rows, name) : undefined;
    if (!forms) {
      expect(status).toBeUndefined();
      continue;
    }
    const exempt = COMPOSER_FORMS[name] ?? 0;
    if (status === OwnerStatus.Composer)
      expect(exempt, `${name} composer exemption`).toBeGreaterThan(0);
    else expect(exempt, `${name} is not a composer`).toBe(0);
    expect(status, `${name} owns a form`).not.toBe(OwnerStatus.NotApplicable);
    const guarded =
      count(text, /onKeyDown=\{guardEnterSubmission\}/g) +
      count(text, /\bguardEnterSubmission\(\s*event\s*\)/g);
    expect(guarded, `${name} guarded forms`).toBe(forms - exempt);
    expect(
      count(text, /\bconsumeFormSubmit\(/g),
      `${name} must claim its own submit event`,
    ).toBeGreaterThanOrEqual(forms - exempt);
  }
  for (const name of Object.keys(COMPOSER_FORMS))
    expect(statusOf(rows, name)).toBe(OwnerStatus.Composer);
});

it("keeps the owner inventory free of issue numbers and planned work", () => {
  const text = readFileSync(CONTRACT, "utf8");
  const start = text.indexOf("#### Owner inventory");
  const section = text.slice(start, text.indexOf("\n### ", start));
  expect(section).not.toMatch(/\b\d{3,}-U\d\b|#\d{3,}|\bplanned\b/);
});
