// SPDX-License-Identifier: Apache-2.0
import { renderHook } from "@testing-library/react";
import { expect, it } from "vitest";
import { FailureCode } from "@delinoio/delidev-api-client";
import { ReadStage } from "./scroll-pagination";
import type { ScrollContinuationQuery } from "./scroll-continuation";
import { useQueueRefreshPresentation } from "./queue-refresh";

type Read = Pick<ScrollContinuationQuery, "loaded" | "loading" | "error">;
it.each([ReadStage.Refresh, ReadStage.Reload])("retains healthy loaded queue display during %s without treating explicit recovery as background", stage => {
 const hook=renderHook((query:Read)=>useQueueRefreshPresentation(query),{initialProps:{loaded:true} as Read});
 hook.rerender({loaded:true,loading:stage});expect(hook.result.current).toBe(true);
 hook.rerender({loaded:true,error:{stage,token:"",failure:{code:FailureCode.CursorExpired,message:"Changed original boundary",guidance:"Reload"}}});expect(hook.result.current).toBe(false);
 hook.rerender({loaded:true,loading:stage});expect(hook.result.current).toBe(false);
 hook.rerender({loaded:true});hook.rerender({loaded:true,loading:stage});expect(hook.result.current).toBe(true);
});
it.each([ReadStage.Initial,ReadStage.Additional,ReadStage.Restore])("preserves visible %s progress",stage=>{
 const hook=renderHook(()=>useQueueRefreshPresentation({loaded:true,loading:stage}));expect(hook.result.current).toBe(false);
});
it("does not treat unaccepted empty loading as retained presentation",()=>{
 const hook=renderHook(()=>useQueueRefreshPresentation({loaded:false,loading:ReadStage.Reload}));expect(hook.result.current).toBe(false);
});
