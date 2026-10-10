// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
// CI-only synthetic browser evidence; not installed CEF/platform acceptance.
export async function assertTabPresentation(page) {
 const result=await page.evaluate(()=>[...document.querySelectorAll('.tab-strip')].filter(root=>root.getClientRects().length).map(root=>{
  const strip=getComputedStyle(root),rect=root.getBoundingClientRect(),scale=parseFloat(getComputedStyle(document.body).zoom)||1;
  return {gap:strip.gap,wrap:strip.flexWrap,baseline:strip.borderBottomWidth,scroll:strip.overflowX,document:document.documentElement.scrollWidth,viewport:innerWidth,scale,items:[...root.querySelectorAll('.tab-item')].map(item=>{
   const style=getComputedStyle(item),underline=getComputedStyle(item,'::after'),box=item.getBoundingClientRect(),label=item.matches('.tab-label')?item:item.querySelector('.tab-label'),close=item.querySelector('.tab-close'),text=getComputedStyle(label),labelBox=label.getBoundingClientRect(),closeBox=close?.getBoundingClientRect();
   return {radius:style.borderRadius,background:style.backgroundColor,border:style.borderRightWidth,height:box.height,labelHeight:labelBox.height,labelRight:labelBox.right,close:closeBox?{width:closeBox.width,height:closeBox.height,left:closeBox.left,right:closeBox.right}:null,right:box.right,font:text.fontSize,line:text.lineHeight,padding:text.paddingLeft,whiteSpace:text.whiteSpace,underline:underline.height,underlineColor:underline.backgroundColor,selected:item.matches('.is-selected,[aria-selected="true"],[aria-current="page"],[data-tab-selected="true"]'),width:box.width,dynamic:item.classList.contains('tab-dynamic')};
  })};
 }));
 assert(result.length,'covered visible tab strip');
 for(const strip of result){assert.equal(strip.gap,'0px');assert.equal(strip.wrap,'nowrap');assert.equal(strip.baseline,'1px');assert.equal(strip.scroll,'auto');assert(strip.document<=strip.viewport+1,'no document horizontal overflow');
  for(const item of strip.items){assert.equal(item.radius,'0px');assert.equal(item.background,'rgba(0, 0, 0, 0)');assert.equal(item.border,'0px');assert(item.labelHeight>=40*strip.scale-1);assert.equal(item.font,'14px');assert.equal(item.line,'20px');assert.equal(item.padding,'16px');assert.equal(item.whiteSpace,'nowrap');assert.equal(item.underline,'2px');if(item.selected)assert.notEqual(item.underlineColor,'rgba(0, 0, 0, 0)');else assert.equal(item.underlineColor,'rgba(0, 0, 0, 0)');if(item.dynamic)assert(item.width<=320*strip.scale+1);if(item.close){assert(item.close.width>=40*strip.scale-1&&item.close.height>=40*strip.scale-1);assert(item.labelRight<=item.close.left+1,'label and close do not overlap');assert(item.close.right<=item.right+1);}}
 }
 const control=page.locator('.tab-strip .tab-label:visible').first();
 // Focus stays independently visible and within the owning strip.
 if(await control.count()){await page.keyboard.press("Tab");await control.focus();const focus=await control.evaluate(node=>{const style=getComputedStyle(node),root=node.closest('.tab-strip'),item=node.closest('.tab-item'),box=item.getBoundingClientRect(),strip=root.getBoundingClientRect();return {width:style.outlineWidth,left:box.left,right:box.right,stripLeft:strip.left,stripRight:strip.right};});assert.equal(focus.width,'2px');assert(focus.left>=focus.stripLeft-1&&focus.right<=focus.stripRight+1,'focused item revealed');}
}
