// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { CodexReasoningDisclosure, codexReasoning } from "./codex-reasoning";
import { TranscriptItem } from "./session";
import { transcriptResource } from "./transcript-role-fixtures";
import { i18n } from "./localization";
import { AppearanceProvider, Theme } from "./appearance";
import { defaultPreferences, DisclosureDefault } from "./appearance-preferences";
const snapshot = (summary: string[] = [], content: string[] = []) => ({kind:"reasoning" as const,text:"" as const,summary,content});
const data = (artifact: object, state = "complete") => ({role:"artifact",text:"",state,artifact});
const open = (container: HTMLElement) => { const trigger = container.querySelector("summary")!; trigger.focus(); fireEvent.click(trigger); return trigger; };

it.each(["en", "ko"])("keeps empty Codex reasoning as a collapsed text-only disclosure in %s", async language => {
 await act(() => i18n.changeLanguage(language));
 const {container}=render(<TranscriptItem resource={transcriptResource(data({started:snapshot(),completed:snapshot()}),20)}/>);
 expect(container.querySelector("details")?.open).toBe(false);
 expect(container.querySelector(".message-codex-reasoning")).not.toBeNull();expect(container.querySelector("header")).toBeNull();
 expect(screen.getByText(language==="ko"?"생각 과정":"Thinking",{selector:"span"})).toBeTruthy();
 open(container);expect(screen.getByText(language==="ko"?"제공된 생각 과정이 없습니다.":"No reasoning content was supplied.")).toBeTruthy();
 expect(container.querySelectorAll("h3")).toHaveLength(0);expect(container.querySelectorAll("pre")).toHaveLength(0);
});

it("selects one original summary while preserving separate snapshots, families, indices and deltas", async()=>{
 await act(() => i18n.changeLanguage("en"));
 const long="  Final 한글🙂\n<script>untrusted()</script>  ";
 const artifact={started:snapshot(["Initial", ""],["  initial content\n"]),completed:snapshot([" \n",long],["final content"]),deltas:[{sequence:3,delta:{kind:"reasoning-summary-added",index:7,text:""}},{sequence:5,delta:{kind:"reasoning-summary",index:7,text:" a "}},{sequence:9,delta:{kind:"reasoning-content",index:0,text:"한글\n🙂"}}]};
 const {container,rerender}=render(<TranscriptItem resource={transcriptResource(data(artifact),21)}/>);
 expect(container.querySelector("summary span")?.textContent).toBe(long);expect(container.querySelector("summary span")?.getAttribute("title")).toBe(long);
 open(container);expect([...container.querySelectorAll("pre")].map(p=>p.textContent)).toEqual(["Initial","","  initial content\n",""," a ","한글\n🙂"," \n",long,"final content"]);
 expect([...container.querySelectorAll("[data-reasoning-kind]")].map(p=>[p.getAttribute("data-reasoning-kind"),p.getAttribute("data-reasoning-index")])).toEqual([["reasoning-summary-added","7"],["reasoning-summary","7"],["reasoning-content","0"]]);
 expect(container.querySelector("script")).toBeNull();expect(container.querySelector("a")).toBeNull();
 const trigger=container.querySelector("summary");trigger?.focus();
 rerender(<TranscriptItem resource={transcriptResource(data({...artifact,completed:snapshot([],[])},"streaming"),21)}/>);
 expect(container.querySelector("summary span")?.textContent).toBe("Initial");expect(container.querySelector("details")?.open).toBe(true);expect(document.activeElement).toBe(trigger);expect(screen.getByText("streaming")).toBeTruthy();
 rerender(<TranscriptItem resource={transcriptResource(data(artifact),21)}/>);expect(container.querySelector("details")?.open).toBe(true);expect(document.activeElement).toBe(trigger);
 await act(() => i18n.changeLanguage("ko"));expect(container.querySelector("summary")).toBe(trigger);expect(container.querySelector("details")?.open).toBe(true);expect(document.activeElement).toBe(trigger);
});

it.each(["interrupted","failed","unavailable","future-state"])("keeps actual %s status without a routine completion label", state=>{
 const {container}=render(<TranscriptItem resource={transcriptResource(data({started:snapshot()},state),22)}/>);
 expect(container.querySelector("header small")?.textContent).toBeTruthy();expect(container.querySelector("summary")).toBeTruthy();
});

it.each([DisclosureDefault.Original,DisclosureDefault.Expanded,DisclosureDefault.Collapsed])("honors initial %s preference and later explicit choice",async preference=>{
 const preferences={...defaultPreferences(),reasoning_disclosure:preference};
 const bridge={read:async()=>({revision:1,theme:Theme.Light,preferences,problem:null}),update:async()=>({revision:1,theme:Theme.Light,preferences,problem:null}),subscribe:async()=>()=>{}};
 const value=codexReasoning(data({started:snapshot(["Original summary"])}))!;
 const view=render(<AppearanceProvider bridge={bridge}><div/></AppearanceProvider>);
 await act(async()=>{});
 view.rerender(<AppearanceProvider bridge={bridge}><CodexReasoningDisclosure value={value} state="streaming"/></AppearanceProvider>);
 await waitFor(()=>expect(view.container.querySelector("details")?.open).toBe(preference===DisclosureDefault.Expanded));
 const node=view.container.querySelector("details")!;const trigger=view.container.querySelector("summary")!;
 fireEvent.click(trigger);expect(node.open).toBe(preference!==DisclosureDefault.Expanded);
 view.rerender(<AppearanceProvider bridge={bridge}><CodexReasoningDisclosure value={{...value,completed:snapshot(["Final summary"])}} state="complete"/></AppearanceProvider>);
 expect(view.container.querySelector("details")).toBe(node);expect(node.open).toBe(preference!==DisclosureDefault.Expanded);
});

it("keeps malformed, mixed and other source families under existing fallback",()=>{
 for(const changed of [{started:{...snapshot(),summary:[null]}},{started:{...snapshot(),text:"foreign"}},{started:{...snapshot(),summary:null}},{started:{...snapshot(),content:["\uD800"]}},{started:{...snapshot(),revision:{}}},{started:snapshot(),completed:{kind:"plan",text:"plan"}},{started:snapshot(),deltas:[{sequence:1,delta:{kind:"reasoning-text",text:"foreign"}}]}]) expect(codexReasoning(data(changed))).toBeUndefined();
 expect(codexReasoning({...data({started:snapshot()}),tool:{}})).toBeUndefined();
 expect(codexReasoning({...data({started:snapshot()}),claude:null})).toBeUndefined();
 const {container}=render(<TranscriptItem resource={transcriptResource(data({started:{...snapshot(),summary:[null]}}),23)}/>);expect(container.querySelector(".message-codex-reasoning")).toBeNull();expect(container.querySelector("article header")).not.toBeNull();
});


it.each(["plan","reasoning-text","opencode-revision","image-generation","future-artifact"])("does not reinterpret %s as indexed Codex reasoning",kind=>{
 expect(codexReasoning(data({started:{...snapshot(),kind}}))).toBeUndefined();
});
it("retains historical context attribution and does not synthesize a summary from deltas",async()=>{
 await act(()=>i18n.changeLanguage("en"));
 const value={...data({started:snapshot(),deltas:[{sequence:2,delta:{kind:"reasoning-summary",index:3,text:"An observed delta is not a snapshot summary"}}]}),context_revision:1};
 const {container}=render(<TranscriptItem resource={transcriptResource(value,24)} contextRevision={2}/>);
 expect(screen.getByText("Earlier context")).toBeTruthy();expect(container.querySelector("summary")?.getAttribute("aria-label")).toBe("Thinking");
 open(container);expect(container.querySelector("pre")?.textContent).toBe("An observed delta is not a snapshot summary");
});
