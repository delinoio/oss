// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { describe, expect, it, vi } from "vitest";
import { GetUsageSummaryResponseSchema, ResourceSchema, UsageCoverage, UsageAccountingProfile } from "@delinoio/delidev-api-client";
import { readLifetimeUsage, sessionCreationMs, usageRangeLimit } from "./session-lifetime-usage";
const reply = (from: bigint, until: bigint, amount = "9007199254740993") => create(GetUsageSummaryResponseSchema,{fromUnixMs:from,untilUnixMs:until,accountingProfile:UsageAccountingProfile.UNSPECIFIED,coverage:UsageCoverage.OBSERVED_ROOT_RESPONSES,totals:{responses:1,input:{knownTotal:amount,measuredResponses:1},output:{knownTotal:"0",measuredResponses:1},cachedInput:{knownTotal:"99",measuredResponses:1},reasoningOutput:{knownTotal:"88",measuredResponses:1}},acceptedExecutionsWithoutResponse:1});
describe("session lifetime response subtotals",()=>{
 it("pins first upper endpoint and serially covers disjoint older ranges with exact subtotals",async()=>{
  const created=1000n, until=created+usageRangeLimit*2n+3000n, firstFrom=until-3000n;let active=0,maximum=0;const calls:bigint[][]=[];
  const read=async(from:bigint,to:bigint)=>{active++;maximum=Math.max(maximum,active);calls.push([from,to]);await Promise.resolve();active--;return reply(from||firstFrom,to||until);};
  const result=await readLifetimeUsage(created,read);expect(calls).toEqual([[0n,0n],[firstFrom-usageRangeLimit,firstFrom],[created,firstFrom-usageRangeLimit]]);expect(maximum).toBe(1);expect(result.input.known).toBe(27021597764222979n);expect(result.output.known).toBe(0n);expect(result.input.measured).toBe(3n);expect(result.incomplete).toBe(true);expect(result).not.toHaveProperty("acceptedExecutionsWithoutResponse");
 });
 it.each(["zero","absent","unavailable"])("preserves %s evidence",async(kind)=>{
  const value=reply(1000n,2000n,"0");value.acceptedExecutionsWithoutResponse=0;if(kind==="absent")value.totals!.input=undefined;if(kind==="unavailable")value.totals!.input={...value.totals!.input!,knownTotal:"",measuredResponses:0,unavailableResponses:1};const result=await readLifetimeUsage(1000n,async()=>value);expect(result.input.known).toBe(kind==="zero"?0n:undefined);expect(result.incomplete).toBe(kind!=="zero");
 });
 it.each(["profile","bounds","canonical","counts","creation"])("rejects invalid %s without a completed result",async(kind)=>{
  const value=reply(1000n,2000n);if(kind==="profile")value.accountingProfile=UsageAccountingProfile.NATIVE_UNITS_V1;if(kind==="bounds")value.untilUnixMs=value.fromUnixMs;if(kind==="canonical")value.totals!.input!.knownTotal="01";if(kind==="counts")value.totals!.input!.measuredResponses=1.5;await expect(readLifetimeUsage(kind==="creation"?3000n:1000n,async()=>value)).rejects.toThrow();
 });
 it("fences canceled or failed older reads without a partial lifetime result",async()=>{
  const first=reply(2000n,3000n),read=vi.fn().mockResolvedValueOnce(first).mockRejectedValueOnce(new Error("failed"));await expect(readLifetimeUsage(1000n,read)).rejects.toThrow("failed");const controller=new AbortController();await expect(readLifetimeUsage(1000n,async()=>{controller.abort();return first;},controller.signal)).rejects.toThrow("Read canceled");
 });
 it("rejects mismatched echoed older bounds",async()=>{const read=vi.fn().mockResolvedValueOnce(reply(2000n,3000n)).mockResolvedValueOnce(reply(1001n,2000n));await expect(readLifetimeUsage(1000n,read)).rejects.toThrow();});
 it("requires exact valid creation metadata and retains millisecond boundary",()=>{expect(sessionCreationMs(create(ResourceSchema,{createdAt:"1970-01-01T00:00:01.123456789Z"}))).toBe(1123n);expect(()=>sessionCreationMs(create(ResourceSchema))).toThrow();expect(()=>sessionCreationMs(create(ResourceSchema,{createdAt:"2026-02-30T00:00:00Z"}))).toThrow();});
});
