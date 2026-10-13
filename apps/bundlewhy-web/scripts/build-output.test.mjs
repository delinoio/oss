import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const dist = join(root, "dist");
const indexPath = join(dist, "index.html");
const REPORT_MARKER = "<!--bundlewhy:report-->";

function listFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? listFiles(path) : [path];
  });
}

test("build emits exactly one self-contained index.html", () => {
  assert.ok(existsSync(indexPath), "dist/index.html must exist");
  const files = listFiles(dist).map((file) => file.slice(dist.length + 1));
  assert.deepEqual(files, ["index.html"]);
});

test("index.html references no separate or external resources", () => {
  const html = readFileSync(indexPath, "utf8");
  assert.doesNotMatch(html, /<script[^>]*\ssrc\s*=/i, "no external scripts");
  assert.doesNotMatch(
    html,
    /<link[^>]*rel\s*=\s*["']?(stylesheet|preload|prefetch|modulepreload)/i,
    "no linked stylesheets or resource hints",
  );
  assert.doesNotMatch(
    html,
    /\s(src|href)\s*=\s*["']?\s*(https?:)?\/\//i,
    "no absolute or protocol-relative resource URLs",
  );
  assert.match(
    html,
    /<script[^>]*>[\s\S]{1000,}<\/script>/,
    "scripts are inlined",
  );
  assert.match(html, /<style[^>]*>[\s\S]+<\/style>/, "styles are inlined");
});

test("index.html contains the report marker exactly once inside head", () => {
  const html = readFileSync(indexPath, "utf8");
  assert.equal(html.split(REPORT_MARKER).length - 1, 1);
  const head = html.slice(html.indexOf("<head"), html.indexOf("</head>"));
  assert.ok(head.includes(REPORT_MARKER), "marker must be inside <head>");
});
