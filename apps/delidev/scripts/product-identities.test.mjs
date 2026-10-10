// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";

// Node's strip-only loader cannot evaluate enums. Convert this closed enum
// for the focused source test; the application uses the original TS enum.
const source = await readFile(new URL("../src/product-identity.ts", import.meta.url), "utf8");
const executable = stripTypeScriptTypes(source.replace(/export enum ProductIdentityKind \{([\s\S]*?)\n\}/, (_, members) => `export const ProductIdentityKind = {${members.replace(/ = /g, ": ")}};`));
const { ProductIdentityKind: Kind, ProductIdentityNumbers, productDiagnosticPresentation } = await import(`data:text/javascript,${encodeURIComponent(executable)}`);

test("numbers follow original identities across reordering and do not recycle", () => {
  const scope = new ProductIdentityNumbers();
  assert.equal(scope.number(Kind.Project, "original-a"), 1);
  assert.equal(scope.number(Kind.Project, "original-b"), 2);
  assert.equal(scope.number(Kind.Project, "original-b"), 2);
  assert.equal(scope.number(Kind.Project, "original-c"), 3);
  assert.equal(scope.number(Kind.Project, "original-a"), 1);
  assert.equal(scope.number(Kind.Repository, "original-a"), 4);
  assert.equal(new ProductIdentityNumbers().number(Kind.Project, "original-c"), 1);
});
test("owned diagnostic presentation replaces UUIDs without changing its source", () => {
  const original = "Request 123e4567-e89b-12d3-a456-426614174000 failed";
  assert.equal(productDiagnosticPresentation(original, () => "Reference 1"), "Request Reference 1 failed");
  assert.equal(original, "Request 123e4567-e89b-12d3-a456-426614174000 failed");
  assert.equal(productDiagnosticPresentation("ordinary text", () => "Reference 1"), "ordinary text");
});
