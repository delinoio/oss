// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { chromium } from "playwright";
import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";

const [url, directory] = process.argv.slice(2);
if (
  !url ||
  !directory ||
  !["127.0.0.1", "localhost"].includes(new URL(url).hostname)
) {
  throw new Error(
    "Usage: node browser.mjs <loopback-url> <evidence-directory>",
  );
}
const pin = JSON.parse(
  await readFile(new URL("./pin.json", import.meta.url), "utf8"),
);
// Reject unpinned runtimes before running checks, so recorded evidence always
// comes from the pinned browser stack.
if (process.versions.node !== pin.node)
  throw new Error(`Use pinned Node ${pin.node}`);
const playwrightVersion = JSON.parse(
  await readFile(
    createRequire(import.meta.url).resolve("playwright/package.json"),
    "utf8",
  ),
).version;
if (playwrightVersion !== pin.playwright)
  throw new Error(
    `Installed Playwright ${playwrightVersion} does not match pinned ${pin.playwright}`,
  );
const browser = await chromium.launch({ headless: true });
try {
  if (browser.version() !== pin.chromium)
    throw new Error(
      `Chromium ${browser.version()} does not match pinned ${pin.chromium}; run playwright install chromium`,
    );
  // A new directory keeps earlier evidence intact and avoids mixing runs.
  try {
    await mkdir(directory);
  } catch (error) {
    if (error.code === "EEXIST")
      throw new Error("Evidence directory already exists; use a new path");
    throw error;
  }
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
    locale: "en-US",
    acceptDownloads: true,
    serviceWorkers: "block",
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  // Exercise the application's supported download/file-input fallback instead
  // of an OS save picker, which cannot be driven in headless Chromium.
  await page.addInitScript(() => {
    delete window.showSaveFilePicker;
    delete window.showOpenFilePicker;
    let seed = 1327;
    Math.random = () => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      return seed / 4294967296;
    };
  });
  await page.goto(url, { waitUntil: "domcontentloaded" });
  const scene = () =>
    page.evaluate(() =>
      JSON.parse(localStorage.getItem("excalidraw") || "[]").filter(
        (e) => !e.isDeleted,
      ),
    );
  const count = (n) =>
    page.waitForFunction(
      (n) =>
        JSON.parse(localStorage.getItem("excalidraw") || "[]").filter(
          (e) => !e.isDeleted,
        ).length === n,
      n,
    );
  const drag = async (x1, y1, x2, y2) => {
    await page.mouse.move(x1, y1);
    await page.mouse.down();
    await page.mouse.move(x2, y2, { steps: 8 });
    await page.mouse.up();
  };
  // Compare every persisted field except generated identity, sketch randomness,
  // timestamps and mutation counters. Export checks cover deterministic roughness.
  // Import canonicalizes absent bindings from null to an empty list.
  const normalize = (elements) =>
    elements.map(
      ({
        id,
        seed,
        created,
        updated,
        version,
        versionNonce,
        boundElements,
        ...element
      }) => ({ ...element, boundElements: boundElements ?? [] }),
    );
  const observations = {};
  await page.getByTestId("toolbar-rectangle").click();
  await drag(400, 300, 560, 400);
  await count(1);
  assert.equal((await scene())[0].type, "rectangle");
  console.error("checked draw");
  observations.draw = normalize(await scene());
  await page.getByTestId("toolbar-freedraw").click();
  await drag(700, 300, 760, 350);
  await count(2);
  assert.equal((await scene())[1].type, "freedraw");
  await page.getByTestId("toolbar-text").click();
  await page.mouse.click(450, 500);
  await page.keyboard.insertText("typeopt");
  await page.keyboard.press("Escape");
  await count(3);
  await page.mouse.dblclick(475, 510);
  await page.locator("textarea").fill("typeopt check");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() =>
    JSON.parse(localStorage.getItem("excalidraw") || "[]").some(
      (e) => e.text === "typeopt check",
    ),
  );
  console.error("checked text");
  observations.text = normalize(await scene());
  await page.keyboard.press("v");
  await page.mouse.click(480, 300);
  for (let i = 0; i < 6; i++) await page.keyboard.press("Shift+ArrowRight");
  for (let i = 0; i < 4; i++) await page.keyboard.press("Shift+ArrowDown");
  await page.waitForFunction(
    () => JSON.parse(localStorage.getItem("excalidraw") || "[]")[0]?.x === 430,
  );
  console.error("checked move");
  observations.move = normalize(await scene());
  await drag(590, 420, 640, 450);
  await page.waitForFunction(
    () =>
      JSON.parse(localStorage.getItem("excalidraw") || "[]")[0]?.width > 160,
  );
  console.error("checked resize");
  observations.resize = normalize(await scene());
  await page.getByTestId("button-undo").click();
  await page.waitForFunction(
    () =>
      JSON.parse(localStorage.getItem("excalidraw") || "[]")[0]?.width === 160,
  );
  console.error("checked undo");
  observations.undo = normalize(await scene());
  await page.getByTestId("button-redo").click();
  await page.waitForFunction(
    () =>
      JSON.parse(localStorage.getItem("excalidraw") || "[]")[0]?.width > 160,
  );
  console.error("checked redo");
  observations.redo = normalize(await scene());
  assert.deepEqual(observations.redo, observations.resize);
  await page.keyboard.press("Escape");
  await page.getByTestId("main-menu-trigger").click();
  await page.getByTestId("json-export-button").click();
  const [sceneDownload] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Save to file", exact: true }).click(),
  ]);
  const sceneFile = path.resolve(directory, "scene.excalidraw");
  await sceneDownload.saveAs(sceneFile);
  const saved = JSON.parse(await readFile(sceneFile, "utf8"));
  assert.deepEqual(normalize(saved.elements), observations.redo);
  await page.reload({ waitUntil: "domcontentloaded" });
  await count(3);
  console.error("checked reload");
  observations.reload = normalize(await scene());
  assert.deepEqual(observations.reload, observations.redo);
  await page.getByTestId("main-menu-trigger").click();
  await page.getByTestId("load-button").click();
  const [chooser] = await Promise.all([
    page.waitForEvent("filechooser"),
    page.getByRole("button", { name: "Load from file", exact: true }).click(),
  ]);
  // Use fixed sketch seeds for import/export comparison. Fresh drawings use
  // Date.now-seeded roughness, so otherwise identical runs render different paths.
  const importFile = path.resolve(directory, "import.excalidraw");
  await writeFile(
    importFile,
    JSON.stringify({
      ...saved,
      elements: saved.elements.map((element, index) => ({
        ...element,
        seed: 12345 + index,
      })),
    }),
  );
  await chooser.setFiles(importFile);
  await page.waitForFunction(
    () =>
      JSON.parse(localStorage.getItem("excalidraw") || "[]")[0]?.seed === 12345,
  );
  await count(3);
  console.error("checked import");
  observations.import = normalize(await scene());
  assert.deepEqual(observations.import, observations.redo);
  await page.keyboard.press("Escape");
  await page.getByTestId("main-menu-trigger").click();
  await page.getByTestId("image-export-button").click();
  const exports = {};
  for (const extension of ["PNG", "SVG"]) {
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page
        .getByRole("button", { name: `Export to ${extension}`, exact: true })
        .click(),
    ]);
    const output = path.resolve(directory, `scene.${extension.toLowerCase()}`);
    await download.saveAs(output);
    const bytes = await readFile(output);
    assert.ok(bytes.length > 100);
    if (extension === "PNG") {
      assert.equal(bytes.subarray(1, 4).toString(), "PNG");
      exports.png = {
        width: bytes.readUInt32BE(16),
        height: bytes.readUInt32BE(20),
      };
      exports.png.pixels_sha256 = await page.evaluate(async (encoded) => {
        const image = new Image();
        image.src = `data:image/png;base64,${encoded}`;
        await image.decode();
        const canvas = document.createElement("canvas");
        canvas.width = image.width;
        canvas.height = image.height;
        const context = canvas.getContext("2d");
        context.drawImage(image, 0, 0);
        const hash = await crypto.subtle.digest(
          "SHA-256",
          context.getImageData(0, 0, image.width, image.height).data,
        );
        return Array.from(new Uint8Array(hash), (byte) =>
          byte.toString(16).padStart(2, "0"),
        ).join("");
      }, bytes.toString("base64"));
    } else {
      exports.svg = await page.evaluate((svg) => {
        const document = new DOMParser().parseFromString(svg, "image/svg+xml");
        return {
          viewBox: document.documentElement.getAttribute("viewBox"),
          geometry: [
            ...document.querySelectorAll(
              "g,path,rect,ellipse,line,polyline,polygon,text",
            ),
          ].map((node) => ({
            tag: node.tagName,
            attributes: Object.fromEntries(
              [...node.attributes]
                .filter((a) => !["id"].includes(a.name))
                .map((a) => [a.name, a.value]),
            ),
            text: node.tagName === "text" ? node.textContent : undefined,
          })),
          paths: document.querySelectorAll("path").length,
          text: [...document.querySelectorAll("text")].map(
            (node) => node.textContent,
          ),
        };
      }, bytes.toString());
      assert.ok(exports.svg.paths > 0);
    }
  }
  assert.deepEqual(errors, []);
  await writeFile(
    path.join(directory, "browser.json"),
    JSON.stringify(
      {
        schema_version: 0,
        node: process.versions.node,
        playwright: playwrightVersion,
        browser: browser.version(),
        observations,
        exports,
        // Per-run checks only; acceptance requires compare-browser.mjs.
        checks_passed: true,
      },
      null,
      2,
    ),
  );
  console.log(
    JSON.stringify({
      browser: browser.version(),
      checks_passed: true,
      checks: Object.keys(observations),
      png: exports.png,
      svg_paths: exports.svg.paths,
    }),
  );
} finally {
  await browser.close();
}
