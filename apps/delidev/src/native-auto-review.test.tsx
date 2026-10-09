// SPDX-License-Identifier: Apache-2.0
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { NativeAutoReview } from "./native-auto-review";
afterEach(cleanup);
it.each([ ["inProgress","Codex is reviewing this action."],["approved","Codex approved this action."],["denied","Codex denied this action."],["timedOut","The AI approval review timed out."],["aborted","The AI approval review was interrupted."] ])("shows the distinct original %s observation",(status,label)=>{
 render(<NativeAutoReview state="complete" progress={{kind:"codex-auto-review",auto_review:{review_id:"original",target_item_id:null,status,started_at_ms:10,completed_at_ms:status==="inProgress"?null:11}}}/>);
 expect(screen.getByRole("status").textContent).toBe(label);expect(screen.queryByRole("button")).toBeNull();
});
it("rejects mixed or malformed observations without exposing private fields",()=>{
 render(<NativeAutoReview state="complete" progress={{kind:"codex-auto-review",auto_review:{status:"approved",rationale:"private context"}}}/>);
 expect(screen.getByRole("status").textContent).toBe("The approval review observation is unavailable.");expect(screen.queryByText("private context")).toBeNull();
});
