import { expect, test } from "vitest";
import { nativeAppsToolSnapshot } from "./validation.js";
const app = { app_id: "connector", name: "Calendar", tool_name: "read", arguments_present: true, error_present: false };
const completed = () => ({ kind: "native-apps", status: "completed", changes: null, apps: { ...app, result: { content: ['null', '{"type":"resource","uri":"https://example.com/x"}', '"한국어😀"'], structured_content: '123' }, duration_ms: 0 } });
test("accepts original native JSON values/order without interpreting result types", () => { const v = completed(); expect(nativeAppsToolSnapshot(v)).toBe(v); });
test.each(["command", "changes", "read", "shell", "unknown"])("rejects cross-tool %s", key => { expect(nativeAppsToolSnapshot({ ...completed(), [key]: [] })).toBeUndefined(); });
test.each([null, {}, 'not JSON', '"\\ud800"'])("validates result surface %s", value => { const v = completed(); Object.assign(v.apps.result, { content: value }); expect(nativeAppsToolSnapshot(v)).toBeUndefined(); });
test("enforces closed fields and native lifecycle outcomes", () => {
 for (const apps of [{...app,result:null},{...app,error_present:true,result:{content:[]}},{...app,result:{content:[],href:"https://example.com"}},{...app,result:{content:['"x"'.repeat(200000)]}},{...app,result:{content:[]},duration_ms:NaN},{...app,result:{content:[]},duration_ms:2**53}]) expect(nativeAppsToolSnapshot({kind:"native-apps",status:"completed",apps})).toBeUndefined();
 expect(nativeAppsToolSnapshot({kind:"native-apps",status:"running",apps:app})).toBeDefined();
 expect(nativeAppsToolSnapshot({kind:"native-apps",status:"running",apps:{...app,duration_ms:0}})).toBeUndefined();
 expect(nativeAppsToolSnapshot({kind:"native-apps",status:"failed",apps:{...app,error_present:true}})).toBeDefined();
 expect(nativeAppsToolSnapshot({kind:"native-apps",status:"failed",apps:app})).toBeUndefined();
});
