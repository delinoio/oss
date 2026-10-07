// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const app = fileURLToPath(new URL("..", import.meta.url));
const read = path => JSON.parse(readFileSync(join(app, path), "utf8"));
const variables = text => [...text.matchAll(/\{\{\s*([\w]+)\s*\}\}|<(s\d+)\/>/g)].map(match => match[1] ?? match[2]).sort();
const productAttributes = new Set(["aria-label", "aria-description", "title", "placeholder", "alt", "label", "description", "emptyLabel", "resourceLabel", "retryLabel", "scope", "summary", "note"]);
const technicalElements = new Set(["pre", "code", "kbd"]);

// These surfaces contain literal command/native source rather than product copy.
// All user/service data remains dynamic and is never accepted as a translation key.
// Classic and Fine grained are the approved GitHub token-kind button labels in
// both languages; guidance and operation states remain translated.
const technicalLiterals = new Set(["https://example.com/", "col", "row", "github.com", "Checks API", "Active rulesets API", "Commit statuses API", "DeliDev", "GitHub", "Classic", "Fine grained", "Codex", "Claude", "Grok", "OpenCode", "ChatGPT", "English", "한국어", "USD", "SHA-256", "Glob", "Grep", "Worktree", "Write-ahead log", "cacheRead", "cacheWrite", "ci_failure", "head", "created_at", "started_at", "updated_at", "public-repositories", "private-repositories", "selected-repositories", "API", "UTC", "ms"]);

test("both catalogs contain every nonempty key and identical named slots", () => {
  for (const directory of ["src/locales", "locales/native"]) {
    const files = directory === "src/locales" ? readdirSync(join(app, directory, "en")).filter(name => name.endsWith(".json")) : ["native"];
    const seen = new Set();
    for (const file of files) {
      const en = read(directory === "src/locales" ? `${directory}/en/${file}` : `${directory}/en.json`);
      const ko = read(directory === "src/locales" ? `${directory}/ko/${file}` : `${directory}/ko.json`);
      assert.deepEqual(Object.keys(ko).sort(), Object.keys(en).sort(), file);
      for (const [key, original] of Object.entries(en)) {
        assert(!seen.has(key), `Duplicate key: ${key}`); seen.add(key);
        assert.equal(typeof original, "string", key);
        assert.equal(typeof ko[key], "string", key);
        assert(original.trim() && ko[key].trim(), `Empty translation: ${key}`);
        assert.deepEqual(variables(ko[key]), variables(original), `Interpolation mismatch: ${key}`);
        assert(!/<(?!s\d+\/>)|<(s\d+)\/>.*<\1\/>/.test(original), `Invalid/repeated rich slot: ${key}`);
        if (/[A-Za-z]/.test(original) && !technicalLiterals.has(original) && !original.replace(/\{\{\w+\}\}|<s\d+\/>/g, "").split(/[\s\W]+/).filter(Boolean).every(word => technicalLiterals.has(word)) && !/^\s*(?:[\d\W]|\{\{\w+\}\}|<s\d+\/>)*\s*$/.test(original)) {
          assert.notEqual(ko[key], original, `Untranslated product copy: ${key}`);
        }
      }
      for (const key of Object.keys(en).filter(key => key.endsWith("_one"))) {
        assert(key.slice(0, -4) + "_other" in en, `Missing plural: ${key}`);
        assert(key.slice(0, -4) + "_other" in ko, `Missing Korean plural: ${key}`);
      }
    }
  }
});

test("product JSX text and accessible literal attributes come from catalogs", () => {
  const violations = [];
  for (const file of readdirSync(join(app, "src")).filter(file => file.endsWith(".tsx") && !/test|fixture/.test(file))) {
    const source = readFileSync(join(app, "src", file), "utf8");
    const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    function visit(node, technical = false) {
      if (ts.isJsxElement(node)) technical ||= technicalElements.has(node.openingElement.tagName.getText(tree));
      const value = ts.isJsxText(node) ? node.text.trim() : ts.isJsxAttribute(node) && productAttributes.has(node.name.getText(tree)) && node.initializer && ts.isStringLiteral(node.initializer) ? node.initializer.text : "";
      if ((!technical || ts.isJsxAttribute(node)) && /[A-Za-z]/.test(value) && !technicalLiterals.has(value)) violations.push(`${file}:${tree.getLineAndCharacterOfPosition(node.getStart(tree)).line + 1}: ${value}`);
      ts.forEachChild(node, child => visit(child, technical));
    }
    visit(tree);
  }
  assert.deepEqual(violations, [], "Hardcoded product copy");
});

// Keys, class names and business predicates must never depend on copy. This
// guard covers the boundaries most likely to regress during string extraction.
test("translation does not supply reconciliation identity or business values", () => {
  const violations = [];
  const stableAttributes = new Set(["key", "id", "className", "value", "name", "role", "type", "htmlFor", "data-settings-category", "state", "kind", "status"]);
  for (const file of readdirSync(join(app, "src")).filter(file => /\.tsx?$/.test(file) && !/test|fixture|localization/.test(file))) {
    const tree = ts.createSourceFile(file, readFileSync(join(app, "src", file), "utf8"), ts.ScriptTarget.Latest, true);
    function containsCopy(node) {
      if (ts.isCallExpression(node) && ["copy", "ownedMessage", "statusLabel"].includes(node.expression.getText(tree))) return true;
      return ts.forEachChild(node, containsCopy) ?? false;
    }
    function visit(node) {
      if (ts.isJsxAttribute(node) && stableAttributes.has(node.name.getText(tree)) && node.initializer && containsCopy(node.initializer)) {
        // A component's presentation name is distinct from an HTML form name.
        const tag = node.parent.parent.tagName.getText(tree);
        if (!(node.name.getText(tree) === "name" && tag === "EstimateCategory")) violations.push(`${file}: ${node.getText(tree)}`);
      }
      if (ts.isBinaryExpression(node) && [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken].includes(node.operatorToken.kind) && containsCopy(node)) violations.push(`${file}: ${node.getText(tree)}`);
      if (ts.isElementAccessExpression(node) && containsCopy(node.argumentExpression)) violations.push(`${file}: ${node.getText(tree)}`);
      ts.forEachChild(node, visit);
    }
    visit(tree);
  }
  assert.deepEqual(violations, [], "Translated identity or business predicate");
});
