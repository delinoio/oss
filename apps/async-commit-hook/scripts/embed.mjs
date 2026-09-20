import assert from "node:assert/strict";
import { cp, mkdir, readFile, readdir, rm } from "node:fs/promises";
import { relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const source = new URL("../dist/", import.meta.url);
const destination = new URL("../../../cmds/async-commit-hook/internal/webassets/dist/", import.meta.url);
export async function validateBundle(root = source) {
  const files = (await readdir(root, { recursive: true, withFileTypes: true }))
    .filter((entry) => !entry.isDirectory());
  for (const entry of files) assert.ok(entry.isFile(), "UI assets must be regular files");
  const names = files.map((entry) => relative(fileURLToPath(root), resolve(entry.parentPath, entry.name)).replaceAll("\\", "/")).sort();
  assert.ok(names.includes("index.html"), "UI index is missing");
  assert.ok(names.some((name) => name.endsWith(".js")), "UI JavaScript is missing");
  assert.ok(names.some((name) => name.endsWith(".css")), "UI styles are missing");
  for (const name of names) assert.match(name, /^(index\.html|assets\/[A-Za-z0-9_.-]+\.[0-9a-f]{8}\.(js|css)(\.LICENSE\.txt)?)$/, "unexpected UI asset");
  const html = await readFile(new URL("index.html", root), "utf8");
  assert.match(html, /<html lang="en">/);
  assert.doesNotMatch(html, /<script(?![^>]*\bsrc=)|<style\b|\sstyle=|https?:\/\//i);
  const references = [...html.matchAll(/(?:src|href)="\/?(assets\/[^"?]+)"/g)].map((match) => match[1]);
  assert.ok(references.some((name) => name.endsWith(".js")));
  assert.ok(references.some((name) => name.endsWith(".css")));
  for (const name of references) assert.ok(names.includes(name), "referenced UI asset is missing");
  return names;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  // Remove the previous embed first so any build/validation failure cannot leave
  // a stale bundle available to a later Go build.
  await rm(destination, { recursive: true, force: true });
  if (process.argv[2] !== "--clean") {
    await validateBundle();
    await mkdir(destination, { recursive: true });
    await cp(source, destination, { recursive: true, errorOnExist: true });
    console.log("ach UI: verified and prepared embedded assets");
  }
}
