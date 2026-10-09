// SPDX-License-Identifier: Apache-2.0
import { webcrypto } from "node:crypto";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { AttachmentService, newRequestId } from "@delinoio/delidev-api-client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { RetainedImages, imageEntryHandlers } from "./image-attachments";
import { imageDigest } from "./image-input";
const bytes = new Uint8Array([1,2,3]);
beforeEach(() => { vi.stubGlobal("crypto",webcrypto); const BaseURL=URL; vi.stubGlobal("URL",class extends BaseURL { static createObjectURL=vi.fn(()=>"blob:verified"); static revokeObjectURL=vi.fn(); }); vi.spyOn(console,"warn").mockImplementation(()=>{}); });
afterEach(()=>vi.unstubAllGlobals());
it("does not display unverified bytes and retries retained readback explicitly",async()=>{
 const read=vi.fn(async(_request: {sessionId:string;attachmentId:string;offset:bigint;limit:number})=>({data:bytes,sha256:"wrong",complete:true}));
 const transport=createRouterTransport(router=>router.service(AttachmentService,{readAttachment:read}));
 const value=[{id:newRequestId(),machine_id:newRequestId(),media_type:"image/png",byte_length:bytes.length,sha256:await imageDigest(bytes)}];
 const mounted=render(<TransportProvider transport={transport}><RetainedImages value={value} sessionId={newRequestId()} /></TransportProvider>);
 await screen.findByRole("button",{name:"Retry loading image"}); expect(URL.createObjectURL).not.toHaveBeenCalled(); expect(read).toHaveBeenCalledTimes(1);
 read.mockResolvedValue({data:bytes,sha256:await imageDigest(bytes),complete:true}); fireEvent.click(screen.getByRole("button",{name:"Retry loading image"}));
 await screen.findByRole("img",{name:"Image 1"}); expect(read).toHaveBeenCalledTimes(2); expect(read.mock.calls[0][0]).toEqual(read.mock.calls[1][0]);
 mounted.unmount(); await waitFor(()=>expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:verified"));
});
it("leaves text clipboard behavior unchanged and fences file entry while locked",()=>{
 const add=vi.fn(); const draft={controller:{add}} as unknown as Parameters<typeof imageEntryHandlers>[0];
 const preventDefault=vi.fn(); imageEntryHandlers(draft,false).onPaste({clipboardData:{files:[]},preventDefault} as never); expect(preventDefault).not.toHaveBeenCalled();
 imageEntryHandlers(draft,true).onPaste({clipboardData:{files:[new File([bytes],"fixture")]},preventDefault} as never); expect(preventDefault).toHaveBeenCalledOnce(); expect(add).not.toHaveBeenCalled();
});
it("does not read retained originals outside the active viewport",async()=>{
 let observe!: (entries:{isIntersecting:boolean}[])=>void;
 vi.stubGlobal("IntersectionObserver",class { constructor(callback:typeof observe){observe=callback;} observe(){} disconnect(){} });
 const read=vi.fn(async()=>({data:bytes,sha256:await imageDigest(bytes),complete:true}));
 const transport=createRouterTransport(router=>router.service(AttachmentService,{readAttachment:read}));
 const value=[{id:newRequestId(),machine_id:newRequestId(),media_type:"image/png",byte_length:bytes.length,sha256:await imageDigest(bytes)}];
 const id=newRequestId(),view=(active:boolean)=><TransportProvider transport={transport}><RetainedImages value={value} sessionId={id} active={active}/></TransportProvider>;
 const mounted=render(view(true)); expect(read).not.toHaveBeenCalled();
 const {act}=await import("@testing-library/react"); await act(async()=>observe([{isIntersecting:true}])); await screen.findByRole("img",{name:"Image 1"}); expect(read).toHaveBeenCalledOnce();
 await act(async()=>observe([{isIntersecting:false}])); await waitFor(()=>expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:verified")); expect(screen.queryByRole("img")).toBeNull();
 mounted.rerender(view(false)); await act(async()=>observe([{isIntersecting:true}])); expect(read).toHaveBeenCalledOnce();
});

it("keeps compact guidance local, preserves Escape focus and retains inline recovery", async () => {
 const { ImageAttachmentInput } = await import("./image-attachments");
 const remove=vi.fn(), retryCleanup=vi.fn();
 const draft={images:[{key:"original",preview:"blob:verified",ready:false}],busy:false,error:undefined,cleanupPending:1,controller:{remove,retryCleanup,add:vi.fn()}} as unknown as Parameters<typeof ImageAttachmentInput>[0]["draft"];
 render(<ImageAttachmentInput compact draft={draft} disabled={false} available routeReady={false} routeLoading machineId="original-machine" controls={<button type="button">Original queue</button>}><textarea aria-label="Retained input" defaultValue="Original draft" /></ImageAttachmentInput>);
 const plus=screen.getByRole("button",{name:"Attach images"});
 expect(screen.queryByRole("button",{name:"Attachment help"})).toBeNull();
 expect(screen.queryByRole("tooltip")).toBeNull();expect(plus.hasAttribute("title")).toBe(false);
 expect(document.getElementById(plus.getAttribute("aria-describedby")!)?.textContent).toBe("PNG, JPEG or WebP · Up to 8 images, 10 MiB each, 40 MiB total.");
 act(()=>plus.focus());expect(screen.getByRole("tooltip").textContent).toContain("40 MiB total");
 fireEvent.keyDown(plus,{key:"Escape"});
 expect(screen.queryByRole("tooltip")).toBeNull();expect(document.activeElement).toBe(plus);
 fireEvent.pointerEnter(plus.parentElement!);expect(screen.getByRole("tooltip")).toBeTruthy();
 const textarea=screen.getByRole("textbox",{name:"Retained input"});act(()=>textarea.focus());
 fireEvent.keyDown(textarea,{key:"Escape"});expect(screen.queryByRole("tooltip")).toBeNull();expect(document.activeElement).toBe(textarea);
 expect(draft.controller.add).not.toHaveBeenCalled();
 expect(screen.getByRole("img",{name:"Image 1"})).toBeTruthy();
 expect(screen.getByText("Checking the selected image route…")).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Retry image cleanup"}));expect(retryCleanup).toHaveBeenCalledOnce();
 fireEvent.click(screen.getByRole("button",{name:"Remove image 1"}));expect(remove).toHaveBeenCalledWith("original");
 expect(screen.getByRole("textbox",{name:"Retained input"})).toHaveProperty("value","Original draft");
});

it("places the original gated plus in the creation toolbar without help or submission", async () => {
 const { ImageAttachmentInput } = await import("./image-attachments");
 const draft={images:[],busy:false,error:undefined,cleanupPending:0,controller:{add:vi.fn(),remove:vi.fn(),retryCleanup:vi.fn()}} as unknown as Parameters<typeof ImageAttachmentInput>[0]["draft"];
 const submit=vi.fn(), click=vi.spyOn(HTMLInputElement.prototype,"click");
 const view=(disabled=false,available=true)=><form onSubmit={submit}><ImageAttachmentInput draft={draft} disabled={disabled} available={available} routeReady routeLoading={false} machineId="original" creationToolbar={attach=><div className="new-session-selectors">{attach}<label>Agent Worker<select><option>Original Agent</option></select></label></div>} /></form>;
 const mounted=render(view());const plus=screen.getByRole("button",{name:"Attach images"});
 expect(plus.textContent).toBe("+");expect(plus.getAttribute("type")).toBe("button");expect(plus.getAttribute("title")).toBe("Attach images");
 expect(plus.nextElementSibling?.textContent).toContain("Agent Worker");
 expect(screen.queryByRole("button",{name:"Attachment help"})).toBeNull();expect(screen.queryByText(/PNG, JPEG or WebP · Up to/)).toBeNull();
 fireEvent.click(plus);expect(click).toHaveBeenCalledOnce();expect(submit).not.toHaveBeenCalled();
 mounted.rerender(view(true));expect(plus).toHaveProperty("disabled",true);fireEvent.click(plus);expect(click).toHaveBeenCalledOnce();
 mounted.rerender(view(false,false));expect(plus).toHaveProperty("disabled",true);expect(screen.getByText(/Update the server and Runner Device/)).toBeTruthy();
});

it("retains tooltip hover/focus, disabled gates and inactive disposal", async () => {
 const { ImageAttachmentInput } = await import("./image-attachments");
 const add=vi.fn(), picker=vi.spyOn(HTMLInputElement.prototype,"click");
 const draft={images:[],busy:false,cleanupPending:0,controller:{add,remove:vi.fn(),retryCleanup:vi.fn()}} as unknown as Parameters<typeof ImageAttachmentInput>[0]["draft"];
 const submit=vi.fn();
 const view=(active=true,disabled=false,available=true,busy=false)=><form onSubmit={submit}><ImageAttachmentInput compact active={active} draft={{...draft,busy}} disabled={disabled} available={available} routeReady routeLoading={false} machineId="original" /></form>;
 const mounted=render(view());const plus=screen.getByRole("button",{name:"Attach images"}), trigger=plus.parentElement!;
 fireEvent.pointerEnter(trigger);fireEvent.pointerLeave(trigger);fireEvent.pointerEnter(screen.getByRole("tooltip"));
 await new Promise(resolve=>setTimeout(resolve,120));expect(screen.getByRole("tooltip")).toBeTruthy();
 act(()=>plus.focus());fireEvent.pointerLeave(screen.getByRole("tooltip"));
 await act(async()=>{await new Promise(resolve=>setTimeout(resolve,120));});expect(screen.getByRole("tooltip")).toBeTruthy();
 act(()=>plus.blur());expect(screen.queryByRole("tooltip")).toBeNull();
 fireEvent.pointerEnter(trigger);fireEvent.click(plus);expect(picker).toHaveBeenCalledOnce();expect(submit).not.toHaveBeenCalled();expect(add).not.toHaveBeenCalled();
 mounted.rerender(view(false));expect(screen.queryByRole("tooltip")).toBeNull();
 for(const [disabled,available,busy] of [[true,true,false],[false,false,false],[false,true,true]]) {
  mounted.rerender(view(true,disabled,available,busy));const button=screen.getByRole("button",{name:"Attach images"});expect(button).toHaveProperty("disabled",true);
  fireEvent.pointerEnter(button.parentElement!);expect(screen.getByRole("tooltip")).toBeTruthy();fireEvent.click(button);expect(picker).toHaveBeenCalledOnce();
  mounted.rerender(view(false,disabled,available,busy));expect(screen.queryByRole("tooltip")).toBeNull();
 }
 mounted.unmount();expect(screen.queryByRole("tooltip")).toBeNull();
});
