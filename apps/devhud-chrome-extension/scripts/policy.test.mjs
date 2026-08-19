import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
test("manifest and source preserve the least-privilege collection boundary", async () => {
  const packageJson = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
  assert.equal(packageJson.name, "devhud-chrome-extension");
  const worker = await readFile(join(root, "src/service-worker.ts"), "utf8");
  const popup = await readFile(join(root, "src/popup.ts"), "utf8");
  assert.match(worker, /connectNative\(HostName\)/u);
  assert.match(popup, /permissions\.request/u);
  assert.match(popup, /synchronously inside the button gesture/u);
  assert.ok(worker.indexOf('nativeRequest("configure", {})') < worker.indexOf("chrome.scripting.executeScript"));
  assert.doesNotMatch(`${worker}\n${popup}`, /chrome\.(?:cookies|webRequest|debugger|storage)|localStorage|sessionStorage|console\./u);
  assert.doesNotMatch(`${worker}\n${popup}`, /selector/u);
  assert.doesNotMatch(`${worker}\n${popup}`, /<all_urls>/u);
});

test("English and Korean locale keys stay identical", async () => {
  const en = JSON.parse(await readFile(join(root, "public/_locales/en/messages.json"), "utf8"));
  const ko = JSON.parse(await readFile(join(root, "public/_locales/ko/messages.json"), "utf8"));
  assert.deepEqual(Object.keys(en).sort(), Object.keys(ko).sort());
});
