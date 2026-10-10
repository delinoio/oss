// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";

// Node's strip-only loader cannot evaluate enums. Convert this closed enum
// for the focused source test; the application uses the original TS enum.
const source = await readFile(new URL("../../../packages/delidev-api-client/src/product-identity.ts", import.meta.url), "utf8");
const executable = stripTypeScriptTypes(source.replace(/export enum ProductIdentityKind \{([\s\S]*?)\n\}/, (_, members) => `export const ProductIdentityKind = {${members.replace(/ = /g, ": ")}};`));
const { ProductIdentityKind: Kind, ProductIdentityNumbers, productDiagnosticPresentation } = await import(`data:text/javascript,${encodeURIComponent(executable)}`);

test("numbers follow original identities across reordering and do not recycle", () => {
  const scope = new ProductIdentityNumbers();
  assert.equal(scope.number(Kind.Project, "original-a"), 1);
  assert.equal(scope.number(Kind.Project, "original-b"), 2);
  assert.equal(scope.number(Kind.Project, "original-b"), 2);
  assert.equal(scope.number(Kind.Project, "original-c"), 3);
  assert.equal(scope.number(Kind.Project, "original-a"), 1);
  assert.equal(scope.number(Kind.Repository, "original-a"), 1);
  assert.equal(new ProductIdentityNumbers().number(Kind.Project, "original-c"), 1);
});
test("owned diagnostic presentation replaces UUIDs without changing its source", () => {
  const original = "Request 123e4567-e89b-12d3-a456-426614174000 failed";
  assert.equal(productDiagnosticPresentation(original, () => "Reference 1"), "Request Reference 1 failed");
  assert.equal(original, "Request 123e4567-e89b-12d3-a456-426614174000 failed");
  assert.equal(productDiagnosticPresentation("ordinary text", () => "Reference 1"), "ordinary text");
});

const reviewSource = await readFile(new URL("../src/product-configuration-review.ts", import.meta.url), "utf8");
const { productConfigurationReview } = await import(`data:text/javascript,${encodeURIComponent(stripTypeScriptTypes(reviewSource))}`);
test("configuration review hides owned references and preserves user text, options and exact integers", () => {
  const id = "123e4567-e89b-12d3-a456-426614174000";
  const raw = `{"id":"${id}","revision":9007199254740993,"name":"${id}","contents":"${id}","options":{"id":"${id}"},"repositories":["${id}"]}`;
  const projected = productConfigurationReview(raw, () => "Resource 1");
  assert.match(projected, /"id":"Resource 1"/);
  assert.match(projected, /9007199254740993/);
  assert.match(projected, /"repositories":\["Resource 1"\]/);
  assert.equal(JSON.parse(projected).name, id);
  assert.equal(JSON.parse(projected).contents, id);
  assert.equal(JSON.parse(projected).options.id, id);
  assert.equal(raw.includes('"id":"Resource 1"'), false);
});
