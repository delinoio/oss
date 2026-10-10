// SPDX-License-Identifier: Apache-2.0
import {expect,it,vi} from "vitest";
import {revealDesktopTab,revealSelectedDesktopTab} from "./desktop-tabs";
it("reveals shared tabs only in their original horizontal strip",()=>{
 const root=document.createElement("div");root.innerHTML='<div class="desktop-tab-strip"><div class="desktop-tab-item is-selected"><button>Original</button><button>Close</button></div></div>';document.body.append(root);
 const strip=root.firstElementChild as HTMLElement,item=strip.firstElementChild as HTMLElement,close=item.lastElementChild as HTMLElement;
 vi.spyOn(strip,"getBoundingClientRect").mockReturnValue({left:10,right:110} as DOMRect);vi.spyOn(item,"getBoundingClientRect").mockReturnValue({left:100,right:180} as DOMRect);strip.scrollTop=37;
 revealDesktopTab(close);expect(strip.scrollLeft).toBe(70);expect(strip.scrollTop).toBe(37);
 root.hidden=true;revealSelectedDesktopTab(root);expect(strip.scrollLeft).toBe(70);root.hidden=false;
 vi.mocked(item.getBoundingClientRect).mockReturnValue({left:-20,right:60} as DOMRect);revealSelectedDesktopTab(root);expect(strip.scrollLeft).toBe(40);expect(document.activeElement).not.toBe(close);root.remove();
});
