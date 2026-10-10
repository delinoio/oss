import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { NativeAppsTool } from "./native-apps-tool";
const app = {app_id:"01900000-0000-7000-8000-000000000001",name:"Calendar",tool_name:"read",arguments_present:true,error_present:false};
const fixture = () => ({output:null,started:{kind:"native-apps",status:"running",changes:null,apps:{...app}},completed:{kind:"native-apps",status:"completed",changes:null,apps:{...app,result:{content:['{"type":"image","url":"https://example.com/image.png"}','"<script>alert(1)</script>"','"한국어😀"'],structured_content:'null'}}}});
test("displays ordered escaped inert results without identifiers or active content",()=>{
 const fetch=vi.spyOn(globalThis,"fetch"); const {container}=render(<NativeAppsTool tool={fixture()} state="complete"/>);
 expect(container.querySelector("a,img,iframe,audio,video,script,button,input")).toBeNull(); expect(container.textContent).not.toContain(app.app_id);
 expect([...container.querySelectorAll("pre")].map(v=>v.textContent)).toEqual([...fixture().completed.apps.result.content,'null']); expect(fetch).not.toHaveBeenCalled();fetch.mockRestore();
});
test.each(["app_id","name","tool_name","arguments_present"])("rejects changed original %s",key=>{const t=fixture();Object.assign(t.completed.apps,{[key]:key==="arguments_present"?false:"changed"});render(<NativeAppsTool tool={t} state="complete"/>);expect(screen.getByText("This native app observation is unavailable.")).toBeTruthy();});
test.each(["output","inputs","patches","states"])("rejects cross-tool %s",key=>{render(<NativeAppsTool tool={{...fixture(),[key]:[]}} state="complete"/>);expect(screen.getByText("This native app observation is unavailable.")).toBeTruthy();});
