// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ProjectEditTabs, ProjectEditTab } from "./project-edit-tabs";
import { ConfigurationNameFailure } from "./configuration-name-failure";
import { TextField } from "./configuration-fields";
import { copy, i18n, SupportedLanguage } from "./localization";
for (const language of [SupportedLanguage.English, SupportedLanguage.Korean]) {
 it(`associates and focuses the localized repository error (${language})`, async () => {
  await i18n.changeLanguage(language); const error=copy("configuration-fields.repositoryNameConflict");
  render(<TextField label="Name" value=" alpha " nameConflict={error} change={()=>{}} />);
  const field=screen.getByRole("textbox");await waitFor(()=>expect(document.activeElement).toBe(field));expect(field.getAttribute("aria-invalid")).toBe("true");expect(document.getElementById(field.getAttribute("aria-describedby")!)?.textContent).toBe(error);expect((field as HTMLInputElement).value).toBe(" alpha ");
 });
}

it("releases only the verified original terminal name failure", async () => {
 const confirmed=vi.fn(), problem={code:"conflict",cause:"configuration_name_conflict"};
 const view=render(<ConfigurationNameFailure state="failed" verified={false} problem={problem} confirmed={confirmed}/>);expect(confirmed).not.toHaveBeenCalled();
 view.rerender(<ConfigurationNameFailure state="uncertain" verified problem={problem} confirmed={confirmed}/>);expect(confirmed).not.toHaveBeenCalled();
 view.rerender(<ConfigurationNameFailure state="failed" verified problem={{code:"conflict",cause:"revision_changed"}} confirmed={confirmed}/>);expect(confirmed).not.toHaveBeenCalled();
 view.rerender(<ConfigurationNameFailure state="failed" verified problem={problem} confirmed={confirmed}/>);await waitFor(()=>expect(confirmed).toHaveBeenCalledOnce());
});

it("reveals an existing project's name tab without replacing its draft", async () => {
 const panels=(error?: string)=>({[ProjectEditTab.General]:<TextField label="Name" value="Draft" nameConflict={error} change={()=>{}}/>,[ProjectEditTab.Repositories]:<p>Repositories</p>,[ProjectEditTab.Execution]:<p>Execution</p>,[ProjectEditTab.Access]:<p>Access</p>});
 const view=render(<ProjectEditTabs disabled={false} panels={panels()}/>);fireEvent.click(screen.getByRole("tab",{name:"Access"}));
 view.rerender(<ProjectEditTabs disabled={false} panels={panels(copy("configuration-fields.projectNameConflict"))}/>);const name=await screen.findByRole("textbox",{name:"Name"});await waitFor(()=>expect(document.activeElement).toBe(name));expect((name as HTMLInputElement).value).toBe("Draft");
});
