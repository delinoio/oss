// SPDX-License-Identifier: Apache-2.0
// Synthetic browser layout evidence; no native window or provider account is used.
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const directory = await mkdtemp(join(tmpdir(), "delidev-account-format-layout-"));
const screenshots = await mkdtemp(join(tmpdir(), "delidev-account-format-preview-"));
let browser, server;
try {
 const build = await createRsbuild({ cwd: app, rsbuildConfig: { plugins: [pluginReact()], source: { entry: { index: join(app, "src/settings-layout.fixture.tsx") } }, html: { template: join(app, "index.html") }, output: { distPath: { root: directory }, assetPrefix: "/", sourceMap: false, cleanDistPath: true } } });
 await build.build();
 server = createServer(async (request,response) => {
  try {
   const pathname = new URL(request.url,"http://127.0.0.1").pathname;
   const file = resolve(directory,`.${pathname === "/" ? "/index.html" : pathname}`);
   if (!file.startsWith(`${directory}${sep}`)) throw new Error("Invalid fixture path");
   response.setHeader("Content-Type", { ".html":"text/html", ".js":"application/javascript", ".css":"text/css", ".svg":"image/svg+xml" }[extname(file)] ?? "application/octet-stream");
   response.end(await readFile(file));
  } catch { response.writeHead(404); response.end(); }
 });
 await new Promise(done => server.listen(0,"127.0.0.1",done));
 browser = await chromium.launch({ headless:true, ...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL ? { channel:process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL } : {}) });
 const page = await browser.newPage();
 const errors = []; page.on("pageerror",error => errors.push(error.message));
 for (const [width,height] of [[1440,1000],[640,480],[480,320]]) {
  await page.setViewportSize({width,height});
  await page.goto(`http://127.0.0.1:${server.address().port}/?apiFormatEdit=true&populated=true&theme=light`);
  await page.getByRole("button",{name:"Settings",exact:true}).click();
  if (width < 760) await page.getByRole("button",{name:"Open settings categories",exact:true}).click();
  await page.getByRole("button",{name:"AI API Keys",exact:true}).click();
  const opener = page.getByRole("button",{name:"More actions for OpenRouter",exact:true});
  await page.screenshot({path:join(screenshots,`list-${width}.png`)});
  await page.getByRole("button",{name:/^More actions/}).first().waitFor();
  await page.screenshot({path:join(screenshots,`loaded-${width}.png`)});
  await opener.click(); await page.getByRole("button",{name:"Edit preferences",exact:true}).click();
  const dialog = page.getByRole("dialog",{name:"Edit AI account",exact:true});
  await dialog.waitFor();
  await page.screenshot({path:join(screenshots,`dialog-${width}.png`)});

  const select = dialog.getByLabel("API format",{exact:true});
  await select.selectOption("openai-responses");
  assert(await dialog.getByRole("combobox",{name:/^Provider/}).isDisabled());
  assert.equal(await dialog.getByLabel("API key",{exact:true}).count(),0);
  await dialog.getByText("Your saved API key is kept. The new format applies to future executions.",{exact:true}).waitFor();
  assert(await dialog.getByRole("button",{name:"Save changes",exact:true}).isEnabled());
  const layout = await dialog.evaluate(node => {
   const r = node.getBoundingClientRect(), body = node.querySelector(".settings-task-body"), footer = node.querySelector(".settings-task-footer");
   return { width:r.width,height:r.height,top:r.top,overflow:node.scrollWidth > node.clientWidth,scroll:body.scrollHeight > body.clientHeight,footerVisible:footer.getBoundingClientRect().bottom <= r.bottom };
  });
  assert(layout.width <= 768.5 && layout.width <= width - 31 && layout.height <= height - 31 && !layout.overflow && layout.footerVisible,JSON.stringify(layout));
  if (height <= 480) assert(layout.scroll,"Small screens must retain body scrolling");
  await select.focus();
  assert(await select.evaluate(node => node === document.activeElement), "Format select must receive keyboard focus");
  await page.keyboard.press("Tab");
  assert(await dialog.evaluate(node => node.contains(document.activeElement)), "Keyboard focus must stay in the dialog");
  await select.selectOption("openai-responses");
  await page.screenshot({path:join(screenshots,`edit-${width}x${height}.png`)});
  await page.keyboard.press("Escape"); await dialog.waitFor({state:"hidden"});
  assert(await opener.evaluate(node => node === document.activeElement),"Focus must return to the account opener");
 }
 assert.deepEqual(errors,[]);
 process.stdout.write(JSON.stringify({operation:"account-format-layout",viewports:3,screenshots,nativeAcceptance:"not-performed",accountAcceptance:"not-performed",nativeSelectKeystrokes:"headless-macos-unavailable"})+"\n");
} finally {
 await browser?.close();
 if (server) await new Promise(done => server.close(done));
 await rm(directory,{recursive:true,force:true});
}
