// SPDX-License-Identifier: Apache-2.0
// Actual renderer/history geometry against isolated synthetic services; no native acceptance.
import assert from "node:assert/strict";
import { TerminalAction } from "@delinoio/delidev-api-client";
import { cp, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
const app=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const root=resolve(app,"../..");
const pinned="1c78f0ee1eb591c603de2bcf337592feaa59b952";
const revision=execFileSync("git",["rev-parse","HEAD"],{cwd:app,encoding:"utf8"}).trim();
const modulePath=process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const {chromium}=await import(modulePath?pathToFileURL(resolve(modulePath)).href:"playwright");
const directory=await mkdtemp(join(tmpdir(),"delidev-terminal-history-"));
let browser;const reports=[];
try {
 browser=await chromium.launch({headless:true,...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL?{channel:process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL}:{})});
 for(const baseline of [true,false]) {
  const source=join(directory,baseline?"baseline":"corrected"),dist=join(source,"dist");
  await cp(join(app,"src"),join(source,"src"),{recursive:true});
  await cp(join(app,"public"),join(source,"public"),{recursive:true});
  await cp(join(root,"packages/delidev-api-client"),join(source,"api-client"),{recursive:true,filter:path=>!path.split(sep).includes("node_modules")});
  await symlink(join(app,"node_modules"),join(source,"node_modules"),"dir");
  if(baseline)await writeFile(join(source,"src/session-terminals.tsx"),execFileSync("git",["show",`${pinned}:apps/delidev/src/session-terminals.tsx`],{cwd:app}));
  // Both exported variants gain the same read-only inspection of the actual pinned xterm.
  // The inspector exposes viewport/cursor/selection values, never the terminal object or operations.
  const emulator=join(source,"src/terminal-emulator.ts");
  await writeFile(emulator,(await readFile(emulator,"utf8")).replace("terminal.open(host); observer.observe(host);", "terminal.open(host); Object.assign(globalThis.__terminalHistoryFixture, {inspect: () => ({viewport: terminal.buffer.active.viewportY, base: terminal.buffer.active.baseY, cursorX: terminal.buffer.active.cursorX, cursorY: terminal.buffer.active.cursorY, selection: terminal.getSelection()})}); observer.observe(host);"));
  const build=await createRsbuild({cwd:source,rsbuildConfig:{plugins:[pluginReact()],source:{entry:{index:join(source,"src/terminal-history.fixture.tsx")},alias:{"@delinoio/delidev-api-client":join(source,"api-client/dist/index.js")}},html:{template:join(app,"index.html")},output:{distPath:{root:dist},assetPrefix:"/",sourceMap:false}}});await build.build();
  const server=createServer(async(request,response)=>{try{const pathname=new URL(request.url,"http://127.0.0.1").pathname;const file=resolve(dist,`.${pathname==="/"?"/index.html":pathname}`);if(!file.startsWith(`${dist}${sep}`))throw new Error("Invalid path");response.setHeader("Content-Security-Policy","default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'none'; base-uri 'none'");response.setHeader("Content-Type",{".html":"text/html",".js":"application/javascript",".css":"text/css"}[extname(file)]??"application/octet-stream");response.end(await readFile(file));}catch{response.writeHead(404);response.end();}});
  await new Promise(done=>server.listen(0,"127.0.0.1",done));
  try {for(const exited of baseline?[false]:[false,true]) {
   const page=await browser.newPage({viewport:{width:1200,height:720}}),errors=[];page.on("pageerror",error=>errors.push(error.message));
   try {
    await page.goto(`http://127.0.0.1:${server.address().port}/?exited=${exited}`);
    await page.getByRole("tab").click();await page.locator(".xterm").waitFor();
    await page.waitForFunction(()=>window.__terminalHistoryFixture.metrics.watches===1);
    await page.waitForTimeout(400);
    const bounds=await page.locator(".xterm-screen").boundingBox();
    process.stdout.write(JSON.stringify({operation:"terminal-scroll-setup",baseline,exited,bounds,native:await page.evaluate(()=>{const {selection,...value}=window.__terminalHistoryFixture.inspect();return value;})})+"\n");
    if(!baseline) { await page.mouse.move(bounds.x+40,bounds.y+20);await page.mouse.wheel(0,-500);
    await page.waitForFunction(()=>{const value=window.__terminalHistoryFixture.inspect();return value.viewport<value.base;},undefined,{timeout:5000});
    await page.mouse.move(bounds.x+10,bounds.y+9);await page.mouse.down();await page.mouse.move(bounds.x+110,bounds.y+9,{steps:5});await page.mouse.up();
    await page.waitForFunction(()=>window.__terminalHistoryFixture.inspect().selection.length>0); }
    await page.evaluate(()=>{
     const state=window.__terminalHistoryFixture;state.samples=[];
     state.renderer=document.querySelector(".xterm");state.input=document.querySelector(".xterm-helper-textarea");
     state.input.focus();
     const viewport=document.querySelector(".xterm-scrollable-element");if(viewport)viewport.scrollTop=50;
     state.scroll=viewport?.scrollTop;state.native=state.inspect();
     state.timer=setInterval(()=>state.samples.push({height:document.querySelector(".terminal-screen").clientHeight,continuation:document.querySelector(".sidebar-continuation").clientHeight,focus:document.activeElement===state.input,same:document.querySelector(".xterm")===state.renderer,scroll:viewport?.scrollTop,native:state.inspect()}),15);
    });
    await page.waitForTimeout(6500);
    const result=await page.evaluate(()=>{const state=window.__terminalHistoryFixture;clearInterval(state.timer);return {metrics:state.metrics,samples:state.samples,scroll:state.scroll,native:state.native};});
    const heights=[...new Set(result.samples.map(sample=>sample.height))],continuations=[...new Set(result.samples.map(sample=>sample.continuation))],resizes=result.metrics.controls.filter(value=>value.action===TerminalAction.RESIZE);
    assert(result.metrics.reads>=6);assert.equal(result.metrics.watches,1);assert.equal(result.metrics.screens,1);assert.equal(result.metrics.removedScreens,0);
    assert(result.samples.every(sample=>sample.same&&sample.focus));assert.deepEqual(result.metrics.requests,[{epoch:"",afterSequence:"0"}]);
    if(baseline){assert(heights.length>1);assert(continuations.length>1);assert(resizes.length>4);}
    else {assert.equal(heights.length,1);assert.equal(continuations.length,1);assert.equal(resizes.length,exited?0:1);assert(result.samples.every(sample=>sample.scroll===result.scroll));assert(result.samples.every(sample=>sample.native.viewport===result.native.viewport&&sample.native.cursorX===result.native.cursorX&&sample.native.cursorY===result.native.cursorY&&sample.native.selection===result.native.selection));}
    reports.push({baseline,exited,reads:result.metrics.reads,resizeCount:resizes.length,dimensions:[...new Map(resizes.map(({rows,columns})=>[`${rows}:${columns}`,{rows,columns}])).values()],heights,continuations,watches:result.metrics.watches,screens:result.metrics.screens,removedScreens:result.metrics.removedScreens,viewport:result.native.viewport,selectedCharacters:result.native.selection.length});
    process.stdout.write(JSON.stringify({operation:"terminal-history-case",...reports.at(-1)})+"\n");
    if(!baseline&&!exited){
     await page.locator(".xterm-helper-textarea").press("x");await page.waitForFunction(action=>window.__terminalHistoryFixture.metrics.controls.some(value=>value.action===action),TerminalAction.INPUT);
     await page.setViewportSize({width:1200,height:780});await page.setViewportSize({width:1200,height:800});await page.setViewportSize({width:1200,height:820});await page.evaluate(()=>new Promise(done=>requestAnimationFrame(()=>requestAnimationFrame(done))));
     const before=await page.evaluate(()=>window.__terminalHistoryFixture.metrics.controls.length);assert.equal(before,result.metrics.controls.length+1);
     await page.evaluate(()=>window.__terminalHistoryFixture.releaseInput());await page.waitForFunction(count=>window.__terminalHistoryFixture.metrics.controls.length===count+1,before);await page.waitForTimeout(400);
     const controls=await page.evaluate(()=>window.__terminalHistoryFixture.metrics.controls);assert.equal(controls.length,before+1);assert.deepEqual(controls.filter(value=>value.action===TerminalAction.INPUT).map(value=>value.input),[[120]]);assert.equal(controls.at(-1).action,TerminalAction.RESIZE);assert(controls.at(-1).rows>resizes.at(-1).rows);assert.equal(controls.at(-1).columns,resizes.at(-1).columns);
    }
    assert.deepEqual(errors,[]);
   }finally{await page.close();}
  }}finally{await new Promise(done=>server.close(done));}
 }
 process.stdout.write(JSON.stringify({operation:"terminal-history-geometry",revision,pinned,dirty:true,reports,nativeAcceptance:"not-performed",screenshots:"not-performed"})+"\n");
}finally{await browser?.close();await rm(directory,{recursive:true,force:true});}
