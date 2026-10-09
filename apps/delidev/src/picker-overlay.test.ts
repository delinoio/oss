// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { pickerOverlayGeometry } from "./picker-overlay";
const surface = { left: 20, top: 100, right: 380, bottom: 600 };
it("anchors below in available space without crossing the owning surface",()=>{
 expect(pickerOverlayGeometry({left:40,top:150,right:240,bottom:190,width:200},surface,200)).toEqual({left:40,top:194,width:200,maxHeight:280});
});
it("flips above a bottom trigger and reduces height to preserve fixed controls",()=>{
 const value=pickerOverlayGeometry({left:300,top:500,right:500,bottom:550,width:200},surface,500);
 expect(value).toEqual({left:172,top:216,width:200,maxHeight:280});
});
it("contains narrow and short owners and resizes without changing identities",()=>{
 const bounds={left:0,top:0,right:180,bottom:140};
 const value=pickerOverlayGeometry({left:80,top:70,right:400,bottom:110,width:320},bounds,280);
 expect(value.width).toBe(164);expect(value.left).toBe(8);expect(value.top).toBe(8);expect(value.maxHeight).toBe(58);
});
