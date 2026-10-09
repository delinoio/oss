// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { validBranchPrefix } from "./session-defaults";
it("preserves literal separators, empty disable and UTF-8 bounds with Git ref validation",()=>{
 for(const value of ["","delidev/","team","team/","팀/","a".repeat(256)]) expect(validBranchPrefix(value),value).toBe(true);
 for(const value of [" ","team\n","..","team//","/team","-team","team@{","team\\","team/.hidden/","team.lock/","한".repeat(86),"\ud800"]) expect(validBranchPrefix(value),value).toBe(false);
});
