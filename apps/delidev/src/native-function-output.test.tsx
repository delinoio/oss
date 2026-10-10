// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeFunctionOutput, validFunctionOutput } from "./native-function-output";
import { i18n, SupportedLanguage } from "./localization";
const base={native_item_id:"original",name:"fixture_tool",namespace:null};
const progress={kind:"codex-function-output",function_output:{...base,variant:"structured",content:[{type:"input_text",text:"first"},{type:"input_image",image_url:"https://invalid.example/image",detail:"original"},{type:"input_audio",audio_url:"file:///private/fixture"},{type:"encrypted_content"},{type:"input_image",file_id:"original-file"}]}};
it("keeps ordered media references inert without opaque encrypted bytes",()=>{
 const {container}=render(<NativeFunctionOutput state="complete" progress={progress}/>);
 fireEvent.click(screen.getByText(/Function output/));
 expect([...container.querySelectorAll("li")].map(item=>item.textContent)).toEqual(["first","input_imagehttps://invalid.example/imageoriginal","input_audiofile:///private/fixture","Encrypted content (private)","input_imageoriginal-file"]);
 expect(container.querySelectorAll("img,audio,video,a,iframe")).toHaveLength(0);
 expect(container.textContent).not.toContain("private-sentinel");
});
it("retains exact string and namespace and supports Korean",async()=>{
 const p={kind:"codex-function-output",function_output:{...base,namespace:"native namespace",variant:"string",text:"<script>inert</script>",content:null}};
 const v=render(<NativeFunctionOutput state="complete" progress={p}/>);
 expect(v.container.querySelector("script")).toBeNull();expect(screen.getByText("native namespace")).toBeTruthy();
 await i18n.changeLanguage(SupportedLanguage.Korean);expect(screen.getByText(/함수 출력/)).toBeTruthy();
});
it.each([{...progress.function_output,content:[{type:"encrypted_content",encrypted_content:"private-sentinel"}]},{...progress.function_output,content:[{type:"input_image",image_url:"a",file_id:"b"}]},{...progress.function_output,content:[{type:"input_audio",audioUrl:"a"}]},{...progress.function_output,content:[{type:"input_image",image_url:"a",detail:null}]},{...progress.function_output,namespace:undefined}])("refuses malformed projections without displaying private data",value=>{
 const p={kind:"codex-function-output",function_output:value};expect(validFunctionOutput(p)).toBe(false);
 render(<NativeFunctionOutput state="complete" progress={p}/>);expect(screen.getByRole("status").textContent).toBe("Function output unavailable");expect(screen.queryByText("private-sentinel")).toBeNull();
});
it("keeps empty output variants distinct",()=>{expect(validFunctionOutput({kind:"codex-function-output",function_output:{...base,variant:"structured",content:[]}})).toBe(true);expect(validFunctionOutput({kind:"codex-function-output",function_output:{...base,variant:"string",text:"",content:null}})).toBe(true)});

it("closes locally with Escape and restores the disclosure trigger",()=>{
 const {container}=render(<NativeFunctionOutput state="complete" progress={progress}/>);
 const details=container.querySelector("details")!;details.open=true;
 fireEvent.keyDown(details,{key:"Escape"});expect(details.open).toBe(false);expect(document.activeElement).toBe(details.querySelector("summary"));
});
