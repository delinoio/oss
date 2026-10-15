// SPDX-License-Identifier: Apache-2.0
// The Danger tone is an allow-list. This inventory counts Danger sites per
// source file and scans for every module that renders buttons and references a
// forget or revoke operation, so a new destructive control cannot be added
// without a tone decision and a matching contract table row.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const directory = join(process.cwd(), "src");
// Forget (opener and confirmation), Revoke (opener) and the confirmation and
// retry opener that can replay a retained revocation.
const expected: Record<string, number> = { "app.tsx": 5 };
const destructive =
  /Operation\.Revoke|RevokeDevice|\.forget\(|\b(?:forget|revoke|unpair|unlink|delete|remove)[A-Z]\w*Request/;

function sources() {
  return readdirSync(directory).filter(
    (name) =>
      /\.tsx?$/.test(name) &&
      !/\.test\.tsx?$/.test(name) &&
      !/fixture/.test(name) &&
      name !== "action-tone.ts",
  );
}

it("limits the Danger tone to the documented destructive sites", () => {
  const actual: Record<string, number> = {};
  for (const name of sources()) {
    const count = (
      readFileSync(join(directory, name), "utf8").match(
        /ActionTone\.Danger/g,
      ) ?? []
    ).length;
    if (count) actual[name] = count;
  }
  expect(actual).toEqual(expected);
});
it("requires every button module with a destructive operation to carry Danger sites", () => {
  const buttonModules = sources().filter((name) => {
    const source = readFileSync(join(directory, name), "utf8");
    return /<button\b/.test(source) && destructive.test(source);
  });
  expect(buttonModules).toEqual(Object.keys(expected));
});
