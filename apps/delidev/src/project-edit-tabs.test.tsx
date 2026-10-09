// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { ProjectEditTab, ProjectEditTabs, revealProjectInvalidControl } from "./project-edit-tabs";
function fixture(disabled = false) {
 return <form onInvalidCapture={revealProjectInvalidControl}><ProjectEditTabs disabled={disabled} panels={{
 [ProjectEditTab.General]: <label>Name<input required defaultValue="" /></label>,
 [ProjectEditTab.Repositories]: <label>Primary<select required defaultValue=""><option value="">Choose</option><option value="original">Original</option></select></label>,
 [ProjectEditTab.Execution]: <label>Attempts<input required type="number" min={1} max={100} defaultValue={3}/></label>,
 [ProjectEditTab.Access]: <label>Restricted<input type="checkbox" defaultChecked /></label>,
 }}/></form>;
}
it("keeps four ordered panels mounted and preserves drafts and per-panel scroll", () => {
 render(fixture());const tabs=screen.getAllByRole('tab');expect(tabs.map(tab=>tab.textContent)).toEqual(['General','Repositories','Execution','Access']);
 const name=screen.getByLabelText('Name');fireEvent.change(name,{target:{value:'Retained name'}});
 fireEvent.click(tabs[1]);const repositories=screen.getByRole('tabpanel');repositories.scrollTop=37;
 fireEvent.change(screen.getByLabelText('Primary'),{target:{value:'original'}});
 fireEvent.click(tabs[2]);expect(screen.getByRole('tabpanel').getAttribute('aria-labelledby')).toBe(tabs[2].id);
 fireEvent.click(tabs[0]);expect(screen.getByLabelText('Name')).toBe(name);expect((name as HTMLInputElement).value).toBe('Retained name');
 fireEvent.click(tabs[1]);expect(screen.getByRole('tabpanel')).toBe(repositories);expect(repositories.scrollTop).toBe(37);expect((screen.getByLabelText('Primary') as HTMLSelectElement).value).toBe('original');
 expect(document.querySelectorAll('[role=tabpanel]')).toHaveLength(4);
});
it("moves tab focus without activating and keeps read-only navigation during locks", () => {
 render(fixture(true));const tabs=screen.getAllByRole('tab');tabs[0].focus();fireEvent.keyDown(tabs[0],{key:'ArrowRight'});expect(document.activeElement).toBe(tabs[1]);expect(tabs[0].getAttribute('aria-selected')).toBe('true');
 fireEvent.keyDown(tabs[1],{key:'End'});expect(document.activeElement).toBe(tabs[3]);fireEvent.click(tabs[3]);expect(tabs[3].getAttribute('aria-selected')).toBe('true');
 expect((screen.getByLabelText('Restricted') as HTMLInputElement).matches(':disabled')).toBe(true);expect(tabs[3].matches(':disabled')).toBe(false);
 fireEvent.keyDown(tabs[3],{key:'Home'});expect(document.activeElement).toBe(tabs[0]);
});
it("reveals and focuses the first invalid field in a hidden panel", async () => {
 render(fixture());const tabs=screen.getAllByRole('tab');fireEvent.click(tabs[3]);const name=screen.getByLabelText('Name');fireEvent.invalid(name);
 await waitFor(()=>expect(document.activeElement).toBe(name));expect(tabs[0].getAttribute('aria-selected')).toBe('true');
 fireEvent.change(name,{target:{value:'Valid'}});fireEvent.click(tabs[0]);const primary=screen.getByLabelText('Primary');fireEvent.invalid(primary);
 await waitFor(()=>expect(document.activeElement).toBe(primary));expect(tabs[1].getAttribute('aria-selected')).toBe('true');
});
