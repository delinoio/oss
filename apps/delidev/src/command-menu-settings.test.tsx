// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ResourceService, ProviderService } from "@delinoio/delidev-api-client";
import { Settings, type SettingsNavigationEntry } from "./settings";
import { SettingsCategory } from "./settings-category";
import { SettingsSearchTarget } from "./settings-search";
import { MutationIntents } from "./mutation";
import { applicationCommands } from "./command-menu";
function fixture(entry:SettingsNavigationEntry){
 const list=vi.fn(()=>({resources:[]}));const transport=createRouterTransport(router=>{router.service(ResourceService,{listResources:list});router.service(ProviderService,{listProviderInventory:()=>({entries:[],capabilities:[]})});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const consumed=vi.fn();const tree=(entryDestination:SettingsNavigationEntry)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings entryDestination={entryDestination} destinationConsumed={consumed}/></MutationIntents></QueryClientProvider></TransportProvider>;
 return{list,consumed,tree};
}
it('consumes typed target generations and retains same-category DOM and unsaved controls',async()=>{
 const first={category:SettingsCategory.Appearance,target:SettingsSearchTarget.Language,generation:'first'};const value=fixture(first),view=render(value.tree(first));await waitFor(()=>expect(document.activeElement?.getAttribute('data-settings-search-target')).toBe('language'));
 const theme=screen.getByRole('radio',{name:'Dark'});const before=(theme as HTMLSelectElement).value;
 const second={category:SettingsCategory.Appearance,target:SettingsSearchTarget.DateFormat,generation:'second'};view.rerender(value.tree(second));await waitFor(()=>expect(document.activeElement?.getAttribute('data-settings-search-target')).toBe('date-format'));expect(screen.getByRole('radio',{name:'Dark'})).toBe(theme);expect((theme as HTMLSelectElement).value).toBe(before);expect(value.consumed).toHaveBeenCalledTimes(2);
 const focused=document.activeElement;view.rerender(value.tree(second));await act(async()=>{await new Promise(done=>setTimeout(done,30));});expect(document.activeElement).toBe(focused);expect(value.consumed).toHaveBeenCalledTimes(2);
});
it('unavailable static target uses ordinary heading fallback after destination reads',async()=>{const entry={category:SettingsCategory.Diagnostics,target:SettingsSearchTarget.Language,generation:'unavailable'},value=fixture(entry);render(value.tree(entry));await waitFor(()=>expect(document.activeElement).toBe(screen.getByRole('heading',{level:1,name:'Connections'})));expect(screen.getByText('This setting is unavailable here.')).toBeTruthy();expect(value.list).not.toHaveBeenCalled();});
it('catalog selection creates fresh typed requests and routes Search and creation only through original entry callbacks',()=>{const openSettings=vi.fn(),navigateHeader=vi.fn(),newSession=vi.fn(),newGeneralChat=vi.fn(),newProject=vi.fn();const commands=applicationCommands({navigate:vi.fn(),navigateHeader,openSettings,newSession,newGeneralChat,newProject,help:vi.fn()});const language=commands.find(command=>command.value==='settings:appearance:language')!;language.run();language.run();expect(openSettings.mock.calls[0][0]).toMatchObject({category:SettingsCategory.Appearance,target:SettingsSearchTarget.Language});expect(openSettings.mock.calls[0][0].generation).not.toBe(openSettings.mock.calls[1][0].generation);commands.find(command=>command.value==='navigate:search')!.run();expect(navigateHeader).toHaveBeenCalledWith('search');for(const id of ['new-session','new-general-chat','new-project'])commands.find(command=>command.value===`create:${id}`)!.run();expect(newSession).toHaveBeenCalledTimes(1);expect(newGeneralChat).toHaveBeenCalledTimes(1);expect(newProject).toHaveBeenCalledTimes(1);});
it('retains an uncommitted Git policy draft and read identity through same-category static targets',async()=>{
 const first={category:SettingsCategory.GitWorkflow,target:SettingsSearchTarget.AutomaticFetch,generation:'fetch'},value=fixture(first),view=render(value.tree(first));
 const checkbox=await screen.findByRole('checkbox',{name:'Allow automatic fetch before Worktree preparation'});await waitFor(()=>expect(document.activeElement?.getAttribute('data-settings-search-target')).toBe('automatic-fetch'));fireEvent.click(checkbox);const checked=(checkbox as HTMLInputElement).checked,reads=value.list.mock.calls.length;
 view.rerender(value.tree({category:SettingsCategory.GitWorkflow,target:SettingsSearchTarget.Worktree,generation:'worktree'}));await waitFor(()=>expect(document.activeElement?.getAttribute('data-settings-search-target')).toBe('worktree'));expect(screen.getByRole('checkbox',{name:'Allow automatic fetch before Worktree preparation'})).toBe(checkbox);expect((checkbox as HTMLInputElement).checked).toBe(checked);expect(value.list).toHaveBeenCalledTimes(reads);
});
