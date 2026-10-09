// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { ConversationKind, conversationForest, HomeNavigation, navigationRow, revealConversation, type NavigationRow } from "./home-navigation";
function row(values: Record<string, unknown> = {}, id = newRequestId(), projectId = "") {
 return navigationRow(create(ResourceSchema, { id, sessionId:id, projectId, kind:EntityKind.SESSION, schemaVersion:1, revision:9007199254740993n, documentJson:encode({ name:id, workspace:"general-chat", outcome:"running", archive:"active", ...values }) }));
}
it("projects closed conversation kinds and only original safe relationship metadata", () => {
 const parent=newRequestId();
 expect(row().conversationKind).toBe(ConversationKind.GeneralChat);
 for(const workspace of ["local","worktree"]) expect(row({workspace}).conversationKind).toBe(ConversationKind.WorkSession);
 const fork=row({fork:{source_session_id:parent,snapshot:{secret:"not retained"}}});
 expect(fork.conversationKind).toBe(ConversationKind.Fork);expect(fork.parentId).toBe(parent);expect(fork.sidechatParent).toBeUndefined();
 const side=row({fork:{source_session_id:parent,sidechat_parent_snapshot:{configuration:{secret:"not retained"}}}});
 expect(side.conversationKind).toBe(ConversationKind.Sidechat);expect(side.sidechatParent).toBe(parent);expect(side.parentId).toBe(parent);
 expect(side.revision).toBe(9007199254740993n);expect(JSON.stringify({...side,revision:String(side.revision)})).not.toContain("secret");expect(side).not.toHaveProperty("fork");
 for(const values of [{workspace:"future"},{fork:null},{fork:[]},{fork:{}},{fork:{source_session_id:"foreign"}},{fork:{source_session_id:parent,sidechat_parent_snapshot:null}},{fork:{source_session_id:parent,sidechat_parent_snapshot:[]}},{fork:{source_session_id:parent,sidechat_parent_snapshot:{}}}]) expect(row(values).conversationKind).toBe(ConversationKind.Unknown);
});
it("keeps accepted root/sibling order, direct generations and once-only IDs within their scope",()=>{
 const p=row(), f=row({fork:{source_session_id:p.id}}), s=row({fork:{source_session_id:p.id,sidechat_parent_snapshot:{configuration:{}}}}), g=row({fork:{source_session_id:f.id}}),u=row();
 const forest=conversationForest([g,u,s,p,f,f]);
 expect(forest.map(n=>n.row.id)).toEqual([u.id,p.id]);expect(forest[1]!.children.map(n=>n.row.id)).toEqual([s.id,f.id]);expect(forest[1]!.children[1]!.children[0]!.row.id).toBe(g.id);
 expect(conversationForest([f])[0]!.row.id).toBe(f.id);
 const foreign={...p,projectId:newRequestId()};expect(conversationForest([f,foreign]).map(n=>n.row.id)).toEqual([f.id,p.id]);
 const home=new HomeNavigation();home.collapsedConversations.add(p.id);home.collapsedConversations.add(f.id);expect(revealConversation(forest,g.id,home.collapsedConversations)).toBe(true);expect(home.collapsedConversations.size).toBe(0);
 home.collapsedConversations.add(p.id);home.resetSessions();expect(home.collapsedConversations.has(p.id)).toBe(true);expect(new HomeNavigation().collapsedConversations.size).toBe(0);
});
it("breaks only self/cycle presentation links without rewriting provenance",()=>{
 const a=row(),b=row(),c=row();a.parentId=b.id;b.parentId=a.id;c.parentId=c.id;
 const descendant=row({fork:{source_session_id:a.id}});const roots=conversationForest([b,descendant,a,c]);
 expect(roots.map(n=>n.row.id)).toEqual([b.id,a.id,c.id]);expect(roots[1]!.children[0]!.row.id).toBe(descendant.id);expect(a.parentId).toBe(b.id);expect(c.parentId).toBe(c.id);
});
it("traverses deep loaded lineage without a recursive graph walk or inventory cutoff",()=>{
 const rows:NavigationRow[]=[];for(let i=0;i<10000;i++){const next=row();if(i)next.parentId=rows[i-1]!.id;rows.push(next);}
 const roots=conversationForest(rows);expect(roots).toHaveLength(1);const collapsed=new Set(rows.slice(0,-1).map(r=>r.id));expect(revealConversation(roots,rows.at(-1)!.id,collapsed)).toBe(true);expect(collapsed.size).toBe(0);
});
