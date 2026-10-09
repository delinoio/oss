// SPDX-License-Identifier: Apache-2.0
import { expect,it,vi } from "vitest";
import { ShortcutCapture,CaptureOperation,CaptureStatus,type CaptureBridge } from "./shortcut-capture";
it("requires acknowledged native admission and settles the same token before another capture",async()=>{
 let complete!:(v:unknown)=>void;let token="";
 const bridge=vi.fn<CaptureBridge>((operation,value)=>{token=value;return operation===CaptureOperation.Begin?new Promise(resolve=>complete=resolve):Promise.resolve({token:value,status:operation===CaptureOperation.End?CaptureStatus.Released:CaptureStatus.Active,remaining_ms:15000});});
 const owner=new ShortcutCapture(bridge);const begin=owner.begin(7);
 await expect(owner.begin(7)).rejects.toThrow("Original capture");
 complete({token,status:CaptureStatus.Active,remaining_ms:15000});expect((await begin).deadline).toBeGreaterThan(performance.now());
 await owner.end();expect(bridge.mock.calls.map(c=>c[0])).toEqual([CaptureOperation.Begin,CaptureOperation.Inspect,CaptureOperation.End]);
 expect(new Set(bridge.mock.calls.map(c=>c[1])).size).toBe(1);expect(bridge.mock.calls.every(c=>c[2]===7)).toBe(true);
});
it("recovers a lost begin ACK only through inspection and retirement, never a repeated Begin",async()=>{
 const bridge=vi.fn<CaptureBridge>(async(operation,token)=>{if(operation===CaptureOperation.Begin)throw Error("Lost ACK");return{token,status:operation===CaptureOperation.End?CaptureStatus.Released:CaptureStatus.Active,remaining_ms:15000};});
 const owner=new ShortcutCapture(bridge);await expect(owner.begin(2)).rejects.toThrow("Lost ACK");await expect(owner.begin(2)).rejects.toThrow();await owner.end();expect(bridge.mock.calls.map(c=>c[0])).toEqual([CaptureOperation.Begin,CaptureOperation.Inspect,CaptureOperation.End]);
});
it("a lost End ACK retains its original token and requires explicit read-only reinspection",async()=>{
 let ended=false;const bridge=vi.fn<CaptureBridge>(async(operation,token)=>{if(operation===CaptureOperation.End){ended=true;throw Error("Lost ACK");}return{token,status:ended?CaptureStatus.Released:CaptureStatus.Active,remaining_ms:15000};});
 const owner=new ShortcutCapture(bridge);await owner.begin(3);await expect(owner.end()).rejects.toThrow();await expect(owner.begin(3)).rejects.toThrow();
 // Released inspection still sends the idempotent original End to fence a late
 // Begin callback that had not executed when the inspection observed absence.
 bridge.mockImplementation(async(operation,token)=>({token,status:CaptureStatus.Released,remaining_ms:0}));await owner.end();expect(new Set(bridge.mock.calls.map(c=>c[1])).size).toBe(1);
});
it("elapsed or contradictory admission cannot authorize a later captured event",async()=>{
 const owner=new ShortcutCapture(async(_,token)=>({token,status:CaptureStatus.Expired,remaining_ms:0}));await expect(owner.begin(1)).rejects.toThrow("expired");
 const invalid=new ShortcutCapture(async()=>({token:"replacement",status:CaptureStatus.Active,remaining_ms:15000}));await expect(invalid.begin(1)).rejects.toThrow("Invalid capture receipt");
});

it("a retired original token cannot end a freshly admitted capture",async()=>{
 const bridge=vi.fn<CaptureBridge>(async(operation,token)=>({token,status:operation===CaptureOperation.End?CaptureStatus.Released:CaptureStatus.Active,remaining_ms:15000}));
 const owner=new ShortcutCapture(bridge);const old=await owner.begin(1);await owner.end(old.token);const next=await owner.begin(1);const calls=bridge.mock.calls.length;await owner.end(old.token);expect(bridge.mock.calls.length).toBe(calls);await owner.end(next.token);
});
