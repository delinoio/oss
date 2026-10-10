// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AppInformation, AppInformationLink } from "./app-information";
import { i18n } from "./localization";
import { settingsCategories, settingsGroups } from "./settings";
import { SettingsCategory } from "./settings-category";
import { matchSettings, SettingsSearchTarget } from "./settings-search";
import { applicationCommands } from "./command-menu";
afterEach(()=>i18n.changeLanguage("en"));
it.each([['darwin-amd64','macOS · Intel'],['darwin-arm64','macOS · Apple Silicon'],['windows-amd64','Windows · x64'],['windows-arm64','Windows · ARM64'],['linux-amd64','Linux · x64'],['linux-arm64','Linux · ARM64']])("shows only running native context for %s",async(target,label)=>{
 const read=vi.fn(async()=>({current_version:'1.2.3',target}));render(<AppInformation readAppContext={read}/>);
 await screen.findByText(label);expect(screen.getByText('1.2.3')).toBeTruthy();expect(read).toHaveBeenCalledTimes(1);
});
it("distinguishes pending, unavailable and denied context without a server version fallback",async()=>{
 let finish!:(value:{current_version:string;target:string})=>void;
 const view=render(<AppInformation readAppContext={()=>new Promise(yes=>{finish=yes;})}/>);expect(screen.getAllByRole('status').some(node=>node.textContent?.includes('Reading app'))).toBe(true);
 await act(()=>finish({current_version:'0.9.0',target:'darwin-arm64'}));await screen.findByText('0.9.0');
 view.rerender(<AppInformation/>);await waitFor(()=>expect(screen.queryByText('0.9.0')).toBeNull());expect(screen.getAllByText('App information is unavailable.').length).toBeGreaterThan(0);
 view.rerender(<AppInformation readAppContext={async()=>{throw 'permission-denied';}}/>);await screen.findByRole('alert');expect(screen.queryByText('0.9.0')).toBeNull();
});
it("dispatches only closed link actions and exposes failure without automatic retry",async()=>{
 const open=vi.fn(async(_action:AppInformationLink)=>{});render(<AppInformation openAppInformationLink={open}/>);
 for(const [name,action] of [['Official releases',AppInformationLink.Releases],['License',AppInformationLink.License],['Open-source notices',AppInformationLink.Notices]] as const){fireEvent.click(screen.getByRole('link',{name}));await waitFor(()=>expect(open).toHaveBeenLastCalledWith(action));await waitFor(()=>expect(screen.queryByText('Opening in your default browser…')).toBeNull());}
 expect(open).toHaveBeenCalledTimes(3);expect(screen.getByText(/not a complete inventory/)).toBeTruthy();
 open.mockRejectedValueOnce('sidecar-failed');fireEvent.click(screen.getByRole('link',{name:'License'}));await screen.findByRole('alert');expect(open).toHaveBeenCalledTimes(4);
});
it("adds final System category and six static command/search destinations in both languages",async()=>{
 expect(settingsGroups.at(-1)?.categories.at(-1)).toBe(SettingsCategory.AppInformation);
 const actions={navigate:vi.fn(),navigateHeader:vi.fn(),openSettings:vi.fn(),newSession:vi.fn(),newGeneralChat:vi.fn(),newProject:vi.fn(),help:vi.fn()};
 for(const [lang,title] of [['en','App information'],['ko','앱 정보']]){
  await i18n.changeLanguage(lang);expect(settingsCategories[SettingsCategory.AppInformation].label).toBe(title);
  const commands=applicationCommands(actions).filter(row=>row.value.startsWith('settings:app-information:'));expect(commands).toHaveLength(6);commands.find(row=>row.value.endsWith('app-updates'))!.run();expect(actions.openSettings).toHaveBeenLastCalledWith(expect.objectContaining({category:SettingsCategory.AppInformation,target:SettingsSearchTarget.AppUpdates}));
  expect(matchSettings(title,[{category:SettingsCategory.AppInformation,label:title,help:''}])).toHaveLength(6);
 }
});

it("releases only the category destination slot when the information view closes",()=>{
 const slot=vi.fn();const view=render(<AppInformation onAppUpdatesSlot={slot}/>);
 expect(slot).toHaveBeenLastCalledWith(expect.any(HTMLElement));const original=slot.mock.calls[0][0];
 view.rerender(<AppInformation onAppUpdatesSlot={slot}/>);expect(slot).toHaveBeenCalledTimes(1);expect(original.isConnected).toBe(true);
 view.unmount();expect(slot).toHaveBeenLastCalledWith(undefined);
});
