// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, readdir, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { screenshotDirectory, validateScreenshotFiles } from "./image-layout-screenshot-path.mjs";

async function fixture(t) {
  const root = await realpath(await mkdtemp(join(tmpdir(), "delidev-screenshot-path-")));
  t.after(() => rm(root, { recursive: true, force: true }));
  const checkout = join(root, "checkout");
  await mkdir(checkout);
  await writeFile(join(checkout, "original.txt"), "original user content");
  return { root, checkout };
}

test("rejects relative, absolute and normalized checkout destinations without writes", async t => {
  const { root, checkout } = await fixture(t);
  for (const value of ["artifacts", ".", checkout, join(checkout, "new", "nested"), join(root, "outside", "..", "checkout", "images")]) {
    await assert.rejects(screenshotDirectory(value, checkout, checkout), /outside the checkout/);
  }
  assert.deepEqual(await readdir(checkout), ["original.txt"]);
  assert.equal(await readFile(join(checkout, "original.txt"), "utf8"), "original user content");
});

test("rejects existing directory and ancestor symlinks into the checkout", async t => {
  const { root, checkout } = await fixture(t);
  const alias = join(root, "external-alias");
  await symlink(checkout, alias, "junction");
  for (const value of [alias, join(alias, "new", "nested")]) {
    await assert.rejects(screenshotDirectory(value, checkout), /outside the checkout/);
  }
  const outside = join(root, "outside");
  await mkdir(outside);
  await symlink(outside, join(checkout, "external-link"), "junction");
  await assert.rejects(screenshotDirectory(join(checkout, "external-link", "images"), checkout), /outside the checkout/);
  assert.equal(await readFile(join(checkout, "original.txt"), "utf8"), "original user content");
  assert.deepEqual(await readdir(outside), []);
});

test("allows external missing descendants and resolves an external ancestor alias", async t => {
  const { root, checkout } = await fixture(t);
  const outside = join(root, "checkout-sibling");
  await mkdir(outside);
  await writeFile(join(outside, "keep.txt"), "keep");
  const alias = join(root, "safe-alias");
  await symlink(outside, alias, "junction");
  const expected = join(outside, "new", "nested");
  assert.equal(await screenshotDirectory(expected, checkout), expected);
  assert.equal(await screenshotDirectory(join(alias, "new", "nested"), checkout), expected);
  assert.deepEqual(await readdir(outside), ["keep.txt"]);
  assert.equal(await readFile(join(outside, "keep.txt"), "utf8"), "keep");
});

test("unset destinations stay disabled and dangling links fail without writes", async t => {
  const { root, checkout } = await fixture(t);
  assert.equal(await screenshotDirectory(undefined, checkout), undefined);
  assert.equal(await screenshotDirectory("", checkout), undefined);
  const alias = join(root, "dangling");
  await symlink(join(root, "missing"), alias, "junction");
  await assert.rejects(screenshotDirectory(join(alias, "images"), checkout), { code: "ENOENT" });
  assert.deepEqual((await readdir(root)).sort(), ["checkout", "dangling"]);
});

test("rejects existing screenshot output symlinks before capture", async t => {
  const { root, checkout } = await fixture(t);
  const screenshots = join(root, "screenshots");
  await mkdir(screenshots);
  const filename = "en-light-1440-1-images.png";
  await symlink(join(checkout, "original.txt"), join(screenshots, filename), "file");
  await assert.rejects(validateScreenshotFiles(screenshots, [filename]), /cannot be a symbolic link/);
  assert.equal(await readFile(join(checkout, "original.txt"), "utf8"), "original user content");
});

test("layout guard runs before browser import, temporary output and build", async () => {
  const source = await readFile(new URL("./test-image-input-layout.mjs", import.meta.url), "utf8");
  const guard = source.indexOf("const screenshots = await screenshotDirectory(");
  const files = source.indexOf("await validateScreenshotFiles(screenshots, screenshotNames)");
  assert(guard > 0);
  assert(files > guard);
  for (const operation of ["await import(modulePath", "await mkdtemp(", "await mkdir(screenshots", "await createRsbuild("]) assert(files < source.indexOf(operation));
  assert.match(source, /cases:16,creationGuidanceSurfaces:32/);
  assert.match(source, /await browser\?\.close\(\)/);
  assert.match(source, /await rm\(directory,\{recursive:true,force:true\}\)/);
});
