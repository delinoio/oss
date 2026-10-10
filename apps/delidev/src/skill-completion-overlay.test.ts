// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { skillCompletionGeometry } from "./skill-completion-overlay";
const bounds={left:0,top:0,right:640,bottom:480};
it("prefers above with matched width and eight-pixel gap",()=>{
 expect(skillCompletionGeometry({left:80,right:480,top:300,bottom:380,width:400},bounds,220)).toEqual({left:80,top:72,width:400,maxHeight:220});
});
it("flips below when above is too short and constrains either side",()=>{
 expect(skillCompletionGeometry({left:80,right:480,top:60,bottom:100,width:400},bounds,220).top).toBe(108);
 const result=skillCompletionGeometry({left:-10,right:690,top:230,bottom:270,width:700},bounds,220);
 expect(result).toEqual({left:8,top:8,width:624,maxHeight:214});
});
it("bounds zoomed geometry to the owning usable surface",()=>{
 const result=skillCompletionGeometry({left:40,right:440,top:180,bottom:220,width:400},{left:20,top:20,right:460,bottom:300},440,2);
 expect(result.left).toBe(40);expect(result.width).toBe(400);expect(result.top).toBe(28);expect(result.maxHeight).toBe(144);
});
