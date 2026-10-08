// SPDX-License-Identifier: Apache-2.0
import { webcrypto } from "node:crypto";
import { useState, StrictMode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ImageDraftProvider, useImageDraft } from "./image-drafts";
import { MutationIntents } from "./mutation";
const bytes=Uint8Array.from(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9d8AAAAASUVORK5CYII=","base64"));
afterEach(()=>vi.unstubAllGlobals());
it("retains independent drafts through presenter remounts and revokes them on identity replacement",async()=>{
 vi.stubGlobal("crypto",webcrypto); vi.stubGlobal("createImageBitmap",async()=>({width:1,height:1,close:()=>{}}));
 const BaseURL=URL; vi.stubGlobal("URL",class extends BaseURL { static createObjectURL=vi.fn(()=>"blob:retained"); static revokeObjectURL=vi.fn(); });
 const file=new File([bytes],"fixture.png",{type:"image/png"}); Object.defineProperty(file,"arrayBuffer",{value:async()=>bytes.buffer});
 function Composer({scope}:{scope:string}) { const draft=useImageDraft(scope); return <><button onClick={()=>void draft.controller.add([file])}>Add fixture image</button><output data-testid="images">{draft.images.length}</output></>; }
 function Presenters(){const [visible,setVisible]=useState(true),[scope,setScope]=useState("new-session");return <><button onClick={()=>setVisible(value=>!value)}>Navigate fixture</button><button onClick={()=>setScope(value=>value==="new-session"?"new-general-chat":"new-session")}>Switch fixture</button>{visible?<Composer scope={scope}/>:null}</>;}
 const transport=createRouterTransport(()=>{}),client=new QueryClient();
 const view=(identity:string)=><StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents key={identity}><ImageDraftProvider><Presenters/></ImageDraftProvider></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
 const mounted=render(view("original")); fireEvent.click(screen.getByText("Add fixture image")); await waitFor(()=>expect(screen.getByTestId("images").textContent).toBe("1"));
 fireEvent.click(screen.getByText("Navigate fixture")); fireEvent.click(screen.getByText("Navigate fixture")); expect(screen.getByTestId("images").textContent).toBe("1");
 fireEvent.click(screen.getByText("Switch fixture")); expect(screen.getByTestId("images").textContent).toBe("0"); fireEvent.click(screen.getByText("Switch fixture")); expect(screen.getByTestId("images").textContent).toBe("1");
 mounted.rerender(view("replacement")); expect(screen.getByTestId("images").textContent).toBe("0"); await waitFor(()=>expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:retained"));
});
