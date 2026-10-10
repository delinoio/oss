// SPDX-License-Identifier: Apache-2.0
// Synthetic markup and committed CSS geometry only; no native or movement acceptance.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const app=resolve(dirname(fileURLToPath(import.meta.url)),"..");
const modulePath=process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium }=await import(modulePath?pathToFileURL(resolve(modulePath)).href:"playwright");
const css=(await Promise.all(["styles.css","session.css"].map(name=>readFile(resolve(app,"src",name),"utf8")))).join("\n");
const browser=await chromium.launch({headless:true,...(process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL?{channel:process.env.DELIDEV_LAYOUT_BROWSER_CHANNEL}:{})});
try {
 const page=await browser.newPage();
 for(const language of ["en","ko"])for(const theme of ["light","dark"])for(const width of [1200,600])for(const count of [1,3]){
  await page.setViewportSize({width,height:480});
  const row=`<div><article class="queue-item queue-compact-item"><div class="queue-row"><button class="queue-drag-handle">⠿</button><p class="queue-preview">${language==="ko"?"테스트":"Test"}</p><div class="queue-compact-actions"><button>Steer</button><button class="queue-icon">⌫</button><button class="queue-icon">…</button></div></div></article></div>`;
  await page.setContent(`<style>${css} :root { --border: ${theme==="dark"?"#485060":"#d8dee8"}; --surface: ${theme==="dark"?"#202632":"#ffffff"}; --text: ${theme==="dark"?"#ffffff":"#202632"}; }</style><div class="session-workspace" style="display:block;height:auto" data-theme="${theme}"><div class="queue-compact-list"><div data-payload-page="">${row.repeat(count)}</div><div class="sidebar-continuation"></div></div></div>`);
  const geometry=await page.locator(".queue-compact-list").evaluate(list=>{
   const box=list.getBoundingClientRect(),rows=[...list.querySelectorAll(".queue-row")].map(row=>{const rect=row.getBoundingClientRect();return {top:rect.top,bottom:rect.bottom,height:rect.height,centers:[...row.children].map(child=>{const childBox=child.getBoundingClientRect();return(childBox.top+childBox.bottom)/2;})};});
   return {top:rows[0].top-box.top,bottom:box.bottom-rows.at(-1).bottom,height:box.height,rows,sentinel:list.querySelector(".sidebar-continuation").getBoundingClientRect().height};
  });
  assert(Math.abs(geometry.top-geometry.bottom)<=1,JSON.stringify(geometry));assert.equal(geometry.sentinel,0);
  if(width>760)for(const row of geometry.rows){assert.equal(row.height,40);assert(Math.max(...row.centers)-Math.min(...row.centers)<=1,JSON.stringify(row));}
  await page.locator(".queue-compact-list").evaluate(list=>{const target=document.createElement("div");target.className="queue-drop-end queue-insertion";list.insertBefore(target,list.lastElementChild);});
  const drag=await page.locator(".queue-drop-end").evaluate(target=>{const box=target.getBoundingClientRect(),list=target.parentElement.getBoundingClientRect();return{top:box.top,bottom:box.bottom,height:box.height,listBottom:list.bottom,listHeight:list.height};});
  assert.equal(drag.listHeight,geometry.height);assert.equal(drag.height,8);assert(drag.bottom<=drag.listBottom&&drag.top>=drag.listBottom-10,JSON.stringify(drag));
 }
 console.log("Compact waiting queue resting/drag geometry passed for 16 synthetic locale/theme/width/count cases.");
} finally {await browser.close();}
