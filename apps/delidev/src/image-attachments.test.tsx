// SPDX-License-Identifier: Apache-2.0
import { webcrypto } from "node:crypto";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
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
