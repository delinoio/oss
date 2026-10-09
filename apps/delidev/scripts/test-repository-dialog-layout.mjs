// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence only; no native picker, account or Clone is run.
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-repository-dialog-"));
const previews = process.env.DELIDEV_LAYOUT_REPOSITORY_SCREENSHOTS === "1" ? await mkdtemp(join(tmpdir(), "delidev-github-preview-")) : null;
let browser, server, page;
let cases = 0;
const messages = new Map(), catalogs = { en: {}, ko: {} };
for (const file of await readdir(join(app, "src/locales/en"))) {
  if (!file.endsWith(".json")) continue;
  const en = JSON.parse(await readFile(join(app, "src/locales/en", file))), ko = JSON.parse(await readFile(join(app, "src/locales/ko", file)));
  Object.assign(catalogs.en, en); Object.assign(catalogs.ko, ko);
  for (const [key, value] of Object.entries(en)) if (!messages.has(value)) messages.set(value, ko[key]);
}
try {
  const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
  await build.build();
  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url, "http://127.0.0.1").pathname;
      const file = resolve(directory, `.${pathname === "/" ? "/index.html" : pathname}`);
      if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
      response.setHeader("Content-Type", { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml" }[extname(file)] ?? "application/octet-stream");
      response.end(await readFile(file));
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  browser = await chromium.launch({ headless: true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel: process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
  page = await browser.newPage();
  const errors = []; page.on("pageerror", error => errors.push(error.message));
  const origin = `http://127.0.0.1:${server.address().port}`;
  // Half-sized CSS viewports model effective 200% layout. Native/browser chrome
  // zoom and packaged CEF behavior require independent platform acceptance.
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1440, 900], [1280, 820], [960, 640], [640, 480], [480, 320], [320, 240]]) {
    const l = value => language === "ko" ? messages.get(value) ?? value : value;
    const c = name => catalogs[language][`repository-github.${name}`];
    await page.setViewportSize({ width, height });
    await page.goto(`${origin}/?theme=${theme}&repository-pat=true&populated=true&language=${language}`);
    await page.getByRole("button", { name: l("Settings"), exact: true }).click();
    const categories = page.getByRole("button", { name: catalogs[language]["App.open_a007d6"].replace("<s0/>", catalogs[language]["App.extra.a1de4eceaa3b"]), exact: true });
    if (width < 760) await categories.click();
    await page.getByRole("button", { name: l("Repositories"), exact: true }).click();
    const opener = page.getByRole("button", { name: l("Add repository"), exact: true });
    await opener.click();
    const dialog = page.getByRole("dialog", { name: l("Add repository"), exact: true });
    await dialog.waitFor();
    const assertBackdrop=async target=>{
      const color=await target.evaluate(node=>getComputedStyle(node,'::backdrop').backgroundColor);
      const channels=color.match(/rgba\(0, 0, 0, ([\d.]+)\)/);
      // Chromium may serialize the 8-bit alpha with two decimal places.
      assert(channels && Math.abs(Number(channels[1])-31/255)<.005,'Each native modal uses the shared dimming token');
      assert(await target.evaluate(node=>node.matches(':modal') && getComputedStyle(node).opacity==='1' && !getComputedStyle(node).backgroundColor.startsWith('rgba')),'The active surface stays opaque');
    };
    await assertBackdrop(dialog);
    assert.equal(await dialog.getByRole("button", {name:"Cancel",exact:true}).count(),0);
    assert.equal(await dialog.getByRole("button", {name:"Back to repositories",exact:true}).count(),0);
    assert(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).evaluate(node => node === document.activeElement));
    assert(await page.locator(".settings-content h1").filter({ hasText: l("Repositories") }).isVisible());
    assert(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).isVisible());
    assert.equal(await dialog.getByRole("textbox", { name: "Clone to", exact: true }).count(), 0);
    await dialog.getByRole("button", { name: c("choose"), exact: true }).waitFor();
    const layout = await dialog.evaluate(node => {
      const rect = node.getBoundingClientRect(), style = getComputedStyle(node);
      return { width: rect.width, height: rect.height, top: rect.top, padding: style.paddingLeft, overflow: node.scrollWidth > node.clientWidth, scroll: node.querySelector(".settings-task-body").scrollHeight > node.querySelector(".settings-task-body").clientHeight, title: getComputedStyle(node.querySelector("header h2")).fontSize, controls: [...node.querySelectorAll("button,input")].every(control => control.getBoundingClientRect().height >= 39.5) };
    });
    assert(layout.width <= 640.5 && layout.width <= width - 31 && layout.height <= height - 31 && layout.top >= 15 && !layout.overflow && layout.controls, JSON.stringify(layout));
    assert.equal(layout.padding, "0px"); assert.equal(layout.title, "22px");
    if (height <= 480) assert(layout.scroll, "Small viewports require vertical dialog scrolling");
    for (const key of ["Tab", "Shift+Tab"]) for (let step = 0; step < 14; step++) { await page.keyboard.press(key); assert(await dialog.evaluate(node => node.contains(document.activeElement)), "Focus escaped the native dialog"); }
    await dialog.getByRole("textbox", { name: "Git URL", exact: true }).fill("https://github.com/owner/repo.git");
    assert(await dialog.getByRole("button", { name: "Add repository", exact: true }).isEnabled());
    await dialog.getByRole("button", { name: "Clone to this computer (optional)", exact: true }).click();
    await dialog.getByRole("textbox", { name: "Clone to", exact: true }).fill("/parent");
    await dialog.getByText("Final path: /parent/repo", { exact: true }).waitFor();
    await dialog.getByRole("button", { name: "Clone to this computer (optional)", exact: true }).click();
    await dialog.getByRole("textbox", { name: "Repository name", exact: true }).fill("Edited repository name");
    const chooser = dialog.getByRole("button", { name: c("choose"), exact: true });
    await chooser.click();
    const child = page.getByRole("dialog", { name: c("title"), exact: true });
    await child.waitFor(); await assertBackdrop(child); await assertBackdrop(dialog); assert.equal(await page.locator("dialog[open]:not([role=region])").count(), 2);
    const profile = child.getByRole("combobox", { name: c("profile"), exact: true });
    assert(await profile.evaluate(node => node === document.activeElement), "The child profile must receive focus");
    // The profile uses the shared ScrollPicker, not a native select.
    await profile.click();
    const choice=child.getByRole("option",{name:"Fixture GitHub profile",exact:true});
    const profileId=await choice.getAttribute("data-picker-id");await choice.click();
    const repository = child.getByRole("button", { name: `example/desktop ${c("private")}`, exact: true }); await repository.waitFor();
    const childLayout = await child.evaluate(node => {
      const rect = node.getBoundingClientRect(), body = node.querySelector(".repository-github-body"), header = node.querySelector("header").getBoundingClientRect();
      return { width: rect.width, height: rect.height, top: rect.top, overflow: node.scrollWidth > node.clientWidth || body.scrollWidth > body.clientWidth, scroll: body.scrollHeight > body.clientHeight, headerTop: header.top, hasFooter: Boolean(node.querySelector("footer")), title: getComputedStyle(node.querySelector("h2")).fontSize, controls: [...node.querySelectorAll("button,input,select")].every(control => control.getBoundingClientRect().height >= 39.5) };
    });
    assert(childLayout.width <= 560.5 && childLayout.width <= width - 31 && childLayout.height <= height - 47 && childLayout.top >= 23 && !childLayout.overflow && childLayout.controls, JSON.stringify(childLayout));
    assert.equal(childLayout.title, "20px"); assert(childLayout.headerTop >= childLayout.top && !childLayout.hasFooter);
    if (height <= 480) assert(childLayout.scroll, "The child body must scroll on small viewports");
    if (previews && width === 1440 && theme === "light") { const path = join(previews, `${language}.png`); await page.screenshot({ path }); console.log(JSON.stringify({ operation: "repository_dialog_preview", language, path, nativeAcceptance: "not-performed" })); }
    await dialog.getByRole("textbox", { name: "Git URL", exact: true }).evaluate(node => node.focus());
    assert(await child.evaluate(node => node.contains(document.activeElement)), "The parent must be inert below the child");
    for (const key of ["Tab", "Shift+Tab"]) for (let step = 0; step < 12; step++) { await page.keyboard.press(key); assert(await child.evaluate(node => node.contains(document.activeElement)), "Focus escaped the child dialog"); }
    const filter = child.getByRole("textbox", { name: c("filter"), exact: true }); await filter.fill("desktop");
    await page.mouse.click(8, 8); assert.equal(await page.locator("dialog[open]:not([role=region])").count(), 2, "Backdrop clicks must retain both dialogs");
    for (const action of ["Escape", "Close"]) {
      if (action === "Escape") await page.keyboard.press("Escape");
      else await child.getByRole("button", { name: c("close"), exact: true }).click();
      await child.waitFor({ state: "detached" }); assert.equal(await page.locator("dialog[open]:not([role=region])").count(), 1);
      await assertBackdrop(dialog);
      assert(await chooser.evaluate(node => node === document.activeElement), "Child close must restore its opener");
      assert.equal(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).inputValue(), "https://github.com/owner/repo.git");
      assert.equal(await dialog.getByRole("textbox", { name: "Repository name", exact: true }).inputValue(), "Edited repository name");
      await chooser.click(); await child.waitFor();
      assert.equal(await profile.getAttribute("data-value"), profileId); assert.equal(await filter.inputValue(), "desktop");
    }
    await repository.click(); await child.waitFor({ state: "detached" });
    assert.equal(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).inputValue(), "https://github.com/example/desktop.git");
    assert.equal(await dialog.getByRole("textbox", { name: "Repository name", exact: true }).inputValue(), "Edited repository name");
    assert(await chooser.evaluate(node => node === document.activeElement));
    await page.keyboard.press("Escape"); await dialog.waitFor({ state: "detached" });
    assert(await opener.evaluate(node => node === document.activeElement), "Opener focus was not restored");
    await opener.click();
    assert.equal(await dialog.getByRole("textbox", { name: "Git URL", exact: true }).inputValue(), "");
    await dialog.locator(".settings-task-close").click();
    cases++;
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ operation: "repository_dialog_layout", result: "passed", cases, themes: 2, languages: 2, viewports: 6, nestedDialogs: true, nativeAcceptance: "not-performed" }));
} catch (error) {
  console.error(JSON.stringify({ operation: "repository_dialog_layout", result: "failed", cases, visibleButtons: page ? await page.getByRole("button").allTextContents() : [] }));
  throw error;
} finally {
  await browser?.close();
  if (server?.listening) await new Promise(done => server.close(done));
  await rm(directory, { recursive: true, force: true });
}
