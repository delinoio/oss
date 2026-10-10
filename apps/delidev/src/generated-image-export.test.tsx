// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ImageMediaType, newRequestId, type ImageAttachment } from "@delinoio/delidev-api-client";
import { GeneratedImageExport } from "./generated-image-export";
const bridge = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => bridge);
const reference = (): ImageAttachment => ({ $typeName: "delidev.v1.ImageAttachment", id: newRequestId(), machineId: newRequestId(), mediaType: ImageMediaType.PNG, sha256: "a".repeat(64), byteLength: 3n });
it("exports only on explicit action with original immutable reference and bytes", async () => {
 bridge.invoke.mockResolvedValueOnce("saved"); const ref = reference(), sessionId = newRequestId();
 render(<GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={1} active />);
 expect(bridge.invoke).not.toHaveBeenCalled(); fireEvent.click(screen.getByRole("button", { name: "Export original image 1" }));
 await screen.findByText("Original image saved.");
 expect(bridge.invoke).toHaveBeenCalledWith("export_generated_image", { request: { operationId: expect.any(String), sessionId, attachmentId: ref.id, sha256: ref.sha256, byteLength: 3, png: "AQID" } }); bridge.invoke.mockClear();
});
it("observes the original operation after a lost acknowledgment without replaying Save", async () => {
 bridge.invoke.mockRejectedValueOnce(new Error("ack lost")); const ref = reference(), sessionId = newRequestId();
 const view = () => <GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={2} active />;
 const first = render(view()); fireEvent.click(screen.getByRole("button", { name: "Export original image 2" }));
 await screen.findByText("The save result is unknown. Check the original save before exporting again.");
 const operationId = bridge.invoke.mock.calls.at(-1)?.[1].request.operationId;
 first.unmount(); render(view()); expect((screen.getByRole("button", { name: "Export original image 2" }) as HTMLButtonElement).disabled).toBe(true);
 bridge.invoke.mockResolvedValueOnce("saved"); fireEvent.click(screen.getByRole("button", { name: "Check original save" }));
 await screen.findByText("Original image saved."); expect(bridge.invoke).toHaveBeenLastCalledWith("read_generated_image_export", { operationId });
 await waitFor(() => expect(bridge.invoke.mock.calls.filter(([command]) => command === "export_generated_image").length).toBe(1)); bridge.invoke.mockClear();
});

it("releases only a correlated no-receipt rejection while another window remains pending, including remount", async () => {
 bridge.invoke.mockReset();
 const a=reference(), b=reference(), sessionId=newRequestId();
 let finishA!:(value:string)=>void;
 let pending=true;
 bridge.invoke.mockImplementation(async(command,args)=>{
  if(command!=="export_generated_image") throw new Error("Absent operation must not be checked");
  if(args.request.attachmentId===a.id) return await new Promise<string>(resolve=>{finishA=value=>{pending=false;resolve(value);};});
  return pending?{operationId:args.request.operationId,disposition:"not-admitted",classification:"busy",noReceipt:true}:"saved";
 });
 const viewA=()=> <GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={a} sessionId={sessionId} number={3} active />;
 const viewB=()=> <GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={b} sessionId={sessionId} number={4} active />;
 render(viewA());let second=render(viewB());
 fireEvent.click(screen.getByRole("button",{name:"Export original image 3"}));
 fireEvent.click(screen.getByRole("button",{name:"Export original image 4"}));
 await screen.findByText("The image was not saved. Choose a new filename and try again.");
 expect((screen.getByRole("button",{name:"Export original image 4"}) as HTMLButtonElement).disabled).toBe(false);
 const rejectedId=bridge.invoke.mock.calls.at(-1)![1].request.operationId;
 second.unmount();second=render(viewB());
 expect((screen.getByRole("button",{name:"Export original image 4"}) as HTMLButtonElement).disabled).toBe(false);
 await act(async()=>finishA("saved"));
 fireEvent.click(screen.getByRole("button",{name:"Export original image 4"}));
 await waitFor(()=>expect(screen.getAllByText("Original image saved.")).toHaveLength(2));
 expect(bridge.invoke.mock.calls.at(-1)![1].request.operationId).not.toBe(rejectedId);
 expect(bridge.invoke.mock.calls.filter(([command])=>command==="read_generated_image_export")).toHaveLength(0);
 bridge.invoke.mockReset();
});
it.each(["stopped","invalid-input","invalid-evidence"])("permits explicit Save after proved %s non-admission",async classification=>{
 bridge.invoke.mockReset();bridge.invoke.mockImplementationOnce(async(_command,args)=>({operationId:args.request.operationId,disposition:"not-admitted",classification,noReceipt:true}));
 const ref=reference(),sessionId=newRequestId();render(<GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={5} active/>);
 fireEvent.click(screen.getByRole("button",{name:"Export original image 5"}));
 await waitFor(()=>expect((screen.getByRole("button",{name:"Export original image 5"}) as HTMLButtonElement).disabled).toBe(false));
 expect(screen.queryByRole("button",{name:"Check original save"})).toBeNull();bridge.invoke.mockReset();
});
it.each(["busy","permission-denied",{disposition:"not-admitted",classification:"busy",noReceipt:true,operationId:"foreign"},{disposition:"not-admitted",classification:"permission-denied",noReceipt:true}])("keeps generic or uncorrelated rejection %j uncertain",async rejection=>{
 bridge.invoke.mockReset();bridge.invoke.mockRejectedValueOnce(rejection);
 const ref=reference(),sessionId=newRequestId();render(<GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={6} active/>);
 fireEvent.click(screen.getByRole("button",{name:"Export original image 6"}));
 await screen.findByText("The save result is unknown. Check the original save before exporting again.");
 expect((screen.getByRole("button",{name:"Export original image 6"}) as HTMLButtonElement).disabled).toBe(true);
 const operationId=bridge.invoke.mock.calls.at(-1)![1].request.operationId;
 bridge.invoke.mockRejectedValueOnce("invalid-evidence");fireEvent.click(screen.getByRole("button",{name:"Check original save"}));
 await waitFor(()=>expect(bridge.invoke).toHaveBeenLastCalledWith("read_generated_image_export",{operationId}));
 expect((screen.getByRole("button",{name:"Export original image 6"}) as HTMLButtonElement).disabled).toBe(true);bridge.invoke.mockReset();
});
it.each(["no-receipt", "foreign", "unknown", "scope"])("retains malformed fulfilled %s proof as uncertainty",async kind=>{
 bridge.invoke.mockReset();bridge.invoke.mockImplementationOnce(async(_command,args)=>({operationId:kind==="foreign"?newRequestId():args.request.operationId,disposition:"not-admitted",classification:kind==="unknown"?"future":kind==="scope"?"permission-denied":"busy",noReceipt:kind!=="no-receipt"}));
 const ref=reference(),sessionId=newRequestId();render(<GeneratedImageExport bytes={new Uint8Array([1,2,3])} reference={ref} sessionId={sessionId} number={7} active/>);
 fireEvent.click(screen.getByRole("button",{name:"Export original image 7"}));
 await screen.findByText("The save result is unknown. Check the original save before exporting again.");
 expect((screen.getByRole("button",{name:"Export original image 7"}) as HTMLButtonElement).disabled).toBe(true);bridge.invoke.mockReset();
});
