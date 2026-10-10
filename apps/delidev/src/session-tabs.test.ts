import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {describe,it,expect} from "vitest";
import {SessionTabsStore,SessionTabKind,sessionTabKey} from "./session-tabs";
import {Comparison} from "./session-diff-model";
describe("connection session tab descriptors",()=>{
 it("pins Conversation and preserves append order with exact resource identities",()=>{const store=new SessionTabsStore();const file={kind:SessionTabKind.File as const,repository:"repo",path:"a"};store.open("s",file);store.open("s",{...file,path:"b"});store.open("s",file);expect(store.snapshot("s").tabs).toHaveLength(3);expect(store.snapshot("s").selected).toBe(sessionTabKey(file));store.close("s",SessionTabKind.Conversation);expect(store.snapshot("s").tabs[0]?.kind).toBe(SessionTabKind.Conversation);store.open("s",{kind:SessionTabKind.Comparison,repository:"repo",comparison:Comparison.Staged,path:"a"});expect(store.snapshot("s").tabs).toHaveLength(4);});
 it("closes presentation with left fallback and keeps inactive selection",()=>{const s=new SessionTabsStore();s.open("s",{kind:SessionTabKind.Terminal,id:"one"});s.open("s",{kind:SessionTabKind.Terminal,id:"two"});s.close("s",JSON.stringify(["terminal","one"]));expect(s.snapshot("s").selected).toBe(JSON.stringify(["terminal","two"]));s.close("s",JSON.stringify(["terminal","two"]));expect(s.snapshot("s").selected).toBe("conversation");});
 it("selects exact positions1 and9 with no last-tab alias or absent selection",()=>{const s=new SessionTabsStore();for(let n=0;n<10;n++)s.open("s",{kind:SessionTabKind.Terminal,id:String(n)});s.position("s",9);expect(s.snapshot("s").selected).toBe(JSON.stringify(["terminal","7"]));s.position("s",1);expect(s.snapshot("s").selected).toBe("conversation");s.position("other",9);expect(s.snapshot("other").selected).toBe("conversation");s.position("s",10);expect(s.snapshot("s").selected).toBe("conversation");});
 it("scopes order to session/connection and retains original Sidechat parent",()=>{const s=new SessionTabsStore();s.open("parent",{kind:SessionTabKind.Sidechat,id:"child",name:"Child"});expect(s.parent("child")).toBe("parent");expect(s.snapshot("other").tabs).toHaveLength(1);expect(new SessionTabsStore().snapshot("parent").tabs).toHaveLength(1);});
});
it("retains Sidechat parent and safe descriptor after presentation close",()=>{const store=new SessionTabsStore();const child={kind:SessionTabKind.Sidechat as const,id:"child",name:"Original child"};store.open("parent",child);store.close("parent",sessionTabKey(child));expect(store.parent("child")).toBe("parent");expect(store.sidechats("parent")).toEqual([child]);store.open("parent",child);expect(store.snapshot("parent").tabs).toHaveLength(2);});


it("dismisses the selected terminal toward its nearest left terminal across other content tabs", () => {
 const store = new SessionTabsStore();
 store.open("session", { kind: SessionTabKind.Terminal, id: "one" });
 store.open("session", { kind: SessionTabKind.Files });
 store.open("session", { kind: SessionTabKind.Terminal, id: "two" });
 store.open("session", { kind: SessionTabKind.Terminal, id: "three" });
 store.select("session", sessionTabKey({ kind: SessionTabKind.Terminal, id: "two" }));
 store.dismissTerminal("session", "two", "three");
 expect(store.snapshot("session").selected).toBe(sessionTabKey({ kind: SessionTabKind.Terminal, id: "one" }));
 store.dismissTerminal("session", "one");
 expect(store.snapshot("session").selected).toBe(sessionTabKey({ kind: SessionTabKind.Terminal, id: "three" }));
});
it("keeps inactive terminal dismissal selection and uses inventory only without another opened terminal", () => {
 const store = new SessionTabsStore();
 store.open("session", { kind: SessionTabKind.Terminal, id: "one" });
 store.open("session", { kind: SessionTabKind.Files });
 store.dismissTerminal("session", "one", "unopened");
 expect(store.snapshot("session").selected).toBe(SessionTabKind.Files);
 expect(store.snapshot("session").tabs.some(tab => tab.kind === SessionTabKind.Terminal)).toBe(false);
 store.open("session", { kind: SessionTabKind.Terminal, id: "one" });
 store.dismissTerminal("session", "one", "remaining");
 expect(store.snapshot("session").selected).toBe(sessionTabKey({ kind: SessionTabKind.Terminal, id: "remaining" }));
});

it("late original creation receipts cannot reopen a dismissed content tab", () => {
 const store = new SessionTabsStore();
 store.terminalPresentation("session").observe("session", create(ResourceSchema, { id: "original", sessionId: "session", kind: EntityKind.TERMINAL, schemaVersion: 1, revision: 5n, documentJson: encode({ state: "exited", cleanup_verified: true }) }));
 store.open("session", { kind: SessionTabKind.Terminal, id: "original" });
 expect(store.snapshot("session").tabs).toHaveLength(1); expect(store.snapshot("session").selected).toBe(SessionTabKind.Conversation);
});

it("updates only existing exact browser pages without selecting, appending or publishing unchanged labels", () => {
 const store = new SessionTabsStore();
 const page = { kind: SessionTabKind.Page as const, profile: "profile", id: "page", title: "original", label: "https://fixture.test/original" };
 store.open("session", page); store.open("session", { kind: SessionTabKind.Files });
 const before = store.snapshot("session"), selected = before.selected;
 store.updatePage("session", { ...page, title: "fixture.test/redirected", label: "https://fixture.test/redirected" });
 const changed = store.snapshot("session");
 expect(changed.selected).toBe(selected);
 expect(changed.tabs.map(sessionTabKey)).toEqual(before.tabs.map(sessionTabKey));
 expect(changed.tabs[1]).toEqual({ ...page, title: "fixture.test/redirected", label: "https://fixture.test/redirected" });
 store.updatePage("session", changed.tabs[1] as typeof page);
 expect(store.snapshot("session")).toBe(changed);
 store.updatePage("session", { ...page, profile: "foreign" });
 store.updatePage("session", { ...page, id: "foreign" });
 store.updatePage("other-session", page);
 expect(store.snapshot("session")).toBe(changed);
 expect(store.snapshot("other-session").tabs).toHaveLength(1);
});
