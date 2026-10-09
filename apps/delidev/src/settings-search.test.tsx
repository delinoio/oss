// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode, useRef } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { i18n } from "./localization";
import { SettingsCategory } from "./settings-category";
import { matchSettings, SettingsSearch, SettingsSearchFocus, SettingsSearchTarget, type SettingsSearchRequest } from "./settings-search";
afterEach(async()=>{await i18n.changeLanguage("en");});
const categories=[{category:SettingsCategory.Appearance,label:"Appearance",help:"Saved on this computer."},{category:SettingsCategory.GitWorkflow,label:"Git",help:"Worktree and pull request preferences."}];
it("matches only current-language bundled metadata using all case-insensitive NFC whitespace tokens in stable page order",async()=>{
 expect(matchSettings("   \t",categories)).toEqual([]);
 expect(matchSettings(" GIT\nAUTOMATIC  fetch",categories).map(row=>row.target)).toEqual([SettingsSearchTarget.AutomaticFetch]);
 expect(matchSettings("git strategy",categories).map(row=>row.target)).toEqual([SettingsSearchTarget.Session,SettingsSearchTarget.Conflict]);
 expect(matchSettings("system language immediately",categories).map(row=>row.target)).toEqual([SettingsSearchTarget.Language]);
 expect(matchSettings("computer timezone",categories).map(row=>row.target)).toEqual([SettingsSearchTarget.DateFormat]);
 expect(matchSettings("customer-private-name",categories)).toEqual([]);
 expect(matchSettings("selector GitHub numeric",categories)).toEqual([]);
 await i18n.changeLanguage("ko");
 expect(matchSettings("theme",categories)).toEqual([]);
 const rows=matchSettings("테마".normalize("NFD"),[{category:SettingsCategory.Appearance,label:"모양",help:"이 컴퓨터에 저장됩니다."}]);
 expect(rows.map(row=>row.target)).toEqual([SettingsSearchTarget.Theme]);
});
it("retains query and input focus on language updates, clears to the input and rejects IME activation",async()=>{
 const select=vi.fn();render(<SettingsSearch categories={categories} select={select}/>);
 const input=screen.getByRole('searchbox');fireEvent.change(input,{target:{value:'theme'}});input.focus();
 const result=screen.getByRole('button',{name:'Appearance › Theme'});
 fireEvent.compositionStart(input);fireEvent.click(result);expect(select).not.toHaveBeenCalled();fireEvent.compositionEnd(input);fireEvent.click(result);expect(select).toHaveBeenCalledTimes(1);
 input.focus();await act(()=>i18n.changeLanguage('ko'));expect((input as HTMLInputElement).value).toBe('theme');expect(document.activeElement).toBe(input);
 fireEvent.change(input,{target:{value:''}});expect((input as HTMLInputElement).value).toBe('');expect(document.activeElement).toBe(input);
});
function FocusFixture({request,pending=false,present=false}:{request?:SettingsSearchRequest;pending?:boolean;present?:boolean}){
 const root=useRef<HTMLDivElement>(null);return <><SettingsSearchFocus request={request} category={SettingsCategory.GitWorkflow} root={root}/><div ref={root}><h1>Git</h1><input aria-label="User focus"/><span data-settings-search-pending={pending?'true':undefined}/>{present?<details className="server-remediation-details"><summary>Details</summary><label data-settings-search-target="remediation-attempts">Attempts<input defaultValue="7"/></label></details>:null}</div></>;
}
const request={category:SettingsCategory.GitWorkflow,target:SettingsSearchTarget.Attempts,generation:'first'};
it("waits for ordinary reads then reveals only Git presentation details and focuses once without changing values",async()=>{
 const view=render(<FocusFixture request={request} pending/>);expect(screen.queryByText('This setting is unavailable here.')).toBeNull();
 view.rerender(<FocusFixture request={request} present/>);await waitFor(()=>expect(document.activeElement?.getAttribute('data-settings-search-target')).toBe('remediation-attempts'));
 expect(screen.getByText('Details').closest('details')?.open).toBe(true);expect((screen.getByText('Attempts').querySelector('input') as HTMLInputElement).value).toBe('7');
 const user=screen.getByLabelText('User focus');user.focus();view.rerender(<FocusFixture request={request} present/>);await act(async()=>{await new Promise(done=>setTimeout(done,40));});expect(document.activeElement).toBe(user);
});
it("reports missing targets after reads settle and never synthesizes their controls",async()=>{
 const view=render(<FocusFixture request={request} pending/>);view.rerender(<FocusFixture request={request}/>);
 await waitFor(()=>expect(document.activeElement).toBe(screen.getByRole('heading')));expect(screen.getByRole('status').textContent).toBe('This setting is unavailable here.');expect(screen.queryByText('Attempts')).toBeNull();
});
it.each(['focus','pointer','keyboard','navigation'])("cancels pending focus on %s intent before a late read",async(kind)=>{
 const view=render(<FocusFixture request={request} pending/>);const user=screen.getByLabelText('User focus');
 if(kind==='focus')user.focus();if(kind==='pointer')fireEvent.pointerDown(user);if(kind==='keyboard')fireEvent.keyDown(user,{key:'Tab'});
 view.rerender(<FocusFixture request={kind==='navigation'?undefined:request} present/>);
 await act(async()=>{await new Promise(done=>setTimeout(done,40));});expect(document.activeElement?.getAttribute('data-settings-search-target')).not.toBe('remediation-attempts');expect(screen.getByText('Details').closest('details')?.open).toBe(false);
});

function SummaryFixture() { const root=useRef<HTMLDivElement>(null);return <><SettingsSearchFocus request={{category:SettingsCategory.Notifications,target:SettingsSearchTarget.Delivery,generation:'summary'}} category={SettingsCategory.Notifications} root={root}/><div ref={root}><h1>Notifications</h1><details><summary data-settings-search-target="notification-delivery">Delivery guidance</summary><p>Guidance</p></details><button>Other control</button></div></>; }
it("preserves native summary tab order after search focuses delivery guidance",async()=>{render(<SummaryFixture/>);const summary=screen.getByText('Delivery guidance');await waitFor(()=>expect(document.activeElement).toBe(summary));expect(summary.getAttribute('tabindex')).toBeNull();expect(summary.tabIndex).toBe(0);screen.getByRole('button',{name:'Other control'}).focus();expect(summary.tabIndex).toBe(0);expect(summary.closest('details')?.open).toBe(false);});

it("cancels an armed target on locale changes without rearming its generation",async()=>{const view=render(<FocusFixture request={request} pending/>);await act(()=>i18n.changeLanguage('ko'));view.rerender(<FocusFixture request={request} present/>);await act(async()=>{await new Promise(done=>setTimeout(done,40));});expect(document.activeElement?.getAttribute('data-settings-search-target')).not.toBe('remediation-attempts');expect(screen.getByText('Details').closest('details')?.open).toBe(false);await act(()=>i18n.changeLanguage('en'));await act(async()=>{await new Promise(done=>setTimeout(done,40));});expect(screen.getByText('Details').closest('details')?.open).toBe(false);});
it("cancels an armed target on responsive reflow before a late read",async()=>{const view=render(<FocusFixture request={request} pending/>);fireEvent(window,new Event('resize'));view.rerender(<FocusFixture request={request} present/>);await act(async()=>{await new Promise(done=>setTimeout(done,40));});expect(document.activeElement?.getAttribute('data-settings-search-target')).not.toBe('remediation-attempts');expect(screen.getByText('Details').closest('details')?.open).toBe(false);});

it("retains a once-only target across Strict Mode setup cleanup replay",async()=>{render(<StrictMode><FocusFixture request={request} present/></StrictMode>);await waitFor(()=>expect(document.activeElement?.getAttribute('data-settings-search-target')).toBe('remediation-attempts'));expect(screen.getByText('Details').closest('details')?.open).toBe(true);});

it("uses a named icon search field without a visible label or application clear control",async()=>{
 render(<SettingsSearch categories={categories} select={vi.fn()}/>);
 const input=screen.getByRole('searchbox',{name:'Search settings'});
 expect(input.getAttribute('placeholder')).toBe('Search Settings');
 expect(screen.getByRole('heading',{name:'Settings'})).toBeTruthy();
 expect(screen.queryByText('Search settings')).toBeNull();
 expect(screen.queryByRole('button',{name:'Clear search'})).toBeNull();
 expect(document.querySelector('.settings-search-field svg')?.getAttribute('aria-hidden')).toBe('true');
 fireEvent.change(input,{target:{value:'theme'}});
 expect(input.getAttribute('aria-label')).toBe('Search settings');
 fireEvent.change(input,{target:{value:'no-match'}});
 expect(screen.getByRole('status')).toBeTruthy();
 expect(screen.queryByRole('button',{name:'Clear search'})).toBeNull();
 input.focus();await act(()=>i18n.changeLanguage('ko'));
 expect(input.getAttribute('placeholder')).toBe('설정 검색');
 expect(input.getAttribute('aria-label')).toBe('설정 검색');
 expect(document.activeElement).toBe(input);
});

it("keeps a pointer target still until click while keyboard and independent focus clear the pinned header",()=>{
 const activate=vi.fn();const view=render(<div className="sidebar-list"><SettingsSearch categories={categories} select={vi.fn()}><button onClick={activate}><span>Original pointer row</span></button><button>Keyboard row</button></SettingsSearch></div>);
 const scroller=view.container.querySelector<HTMLElement>('.sidebar-list')!,header=view.container.querySelector<HTMLElement>('.settings-search-header')!;
 const pointer=screen.getByRole('button',{name:'Original pointer row'}),keyboard=screen.getByRole('button',{name:'Keyboard row'});
 vi.spyOn(scroller,'getBoundingClientRect').mockReturnValue({top:0,bottom:300} as DOMRect);vi.spyOn(header,'getBoundingClientRect').mockReturnValue({top:0,bottom:100} as DOMRect);
 vi.spyOn(pointer,'getBoundingClientRect').mockReturnValue({top:80,bottom:120} as DOMRect);vi.spyOn(keyboard,'getBoundingClientRect').mockReturnValue({top:80,bottom:120} as DOMRect);
 scroller.scrollTop=200;fireEvent.pointerDown(pointer.querySelector('span')!);pointer.focus();expect(scroller.scrollTop).toBe(200);fireEvent.pointerUp(pointer);expect(scroller.scrollTop).toBe(200);fireEvent.click(pointer);expect(activate).toHaveBeenCalledTimes(1);
 fireEvent.keyDown(pointer,{key:'Tab'});keyboard.focus();expect(scroller.scrollTop).toBe(174);
 fireEvent.pointerDown(pointer);fireEvent.pointerCancel(pointer);pointer.focus();expect(scroller.scrollTop).toBe(148);
});
