// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { PRAvatarCache } from "./pr-avatar-cache";
it("bounds sanitized avatar memory and disposes connection URLs", () => {
 const created:string[]=[],revoked:string[]=[];
 Object.defineProperty(URL,"createObjectURL",{configurable:true,value:vi.fn(() => {const value=`blob:fixture-${created.length}`;created.push(value);return value;})});
 Object.defineProperty(URL,"revokeObjectURL",{configurable:true,value:vi.fn((value:string)=>revoked.push(value))});
 const cache=new PRAvatarCache();for(let i=0;i<65;i++)cache.put(String(i),new Uint8Array([1,2,3]));
 expect(cache.read("0")).toBeUndefined();expect(cache.attempted("0")).toBe(true);expect(cache.read("64")).toBeDefined();expect(revoked).toHaveLength(1);
 cache.put("64",new Uint8Array([4]));expect(created).toHaveLength(65);
 cache.clear();expect(revoked).toHaveLength(65);expect(cache.attempted("64")).toBe(false);
});
