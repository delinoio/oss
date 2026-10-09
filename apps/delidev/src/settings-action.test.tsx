// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation, SettingsActionScope } from "./settings-action";
import { ProjectList } from "./project-list";
import { RepositoryRow } from "./repository-list";
import { AgentWorkerRow } from "./agent-worker-row";
import { SettingsTaskActions } from "./settings-task";
import { copy, i18n, useLocale } from "./localization";
import { encode, resourceName } from "./documents";

it("keeps native form association, ref, disabled guard and one original callback", () => {
  const save = vi.fn(), edit = vi.fn(), ref = vi.fn();
  render(<SettingsActionScope><form id="original" onSubmit={event => { event.preventDefault(); save(); }} /><SettingsTaskActions form="original"><SettingsActionButton icon={SettingsActionIcon.Save}>Save</SettingsActionButton></SettingsTaskActions><SettingsActionButton ref={ref} icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} type="button" disabled onClick={edit}>Edit project</SettingsActionButton></SettingsActionScope>);
  fireEvent.click(screen.getByRole("button", { name: "Save" })); expect(save).toHaveBeenCalledTimes(1);
  const button = screen.getByRole("button", { name: "Edit project" }); fireEvent.click(button); expect(edit).not.toHaveBeenCalled(); expect(ref).toHaveBeenCalledWith(button);
  expect(button.querySelector("svg")?.getAttribute("focusable")).toBe("false"); expect(button.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
});
it("shows the localized tooltip on hover and focus without replacing the opener or replaying work", async () => {
  const action = vi.fn(), focused = vi.fn();
  function View() { useLocale(); const ref = useRef<HTMLButtonElement>(null); return <SettingsActionScope><SettingsActionButton ref={ref} icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} onClick={action} onFocus={focused}>{copy("settings.edit_464c4f")}</SettingsActionButton></SettingsActionScope>; }
  await act(() => i18n.changeLanguage("en")); render(<View />); const button = screen.getByRole("button", { name: "Edit" });
  fireEvent.pointerEnter(button); expect(screen.getByRole("tooltip").textContent).toBe("Edit"); expect(action).not.toHaveBeenCalled(); fireEvent.pointerLeave(button); expect(screen.queryByRole("tooltip")).toBeNull();
  act(() => button.focus()); expect(screen.getByRole("tooltip").textContent).toBe("Edit"); expect(focused).toHaveBeenCalledTimes(1);
  await act(() => i18n.changeLanguage("ko")); expect(screen.getByRole("button", { name: copy("settings.edit_464c4f") })).toBe(button); expect(document.activeElement).toBe(button); expect(screen.getByRole("tooltip").textContent).toBe(copy("settings.edit_464c4f")); expect(action).not.toHaveBeenCalled(); expect(focused).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("tooltip").getAttribute("tabindex")).toBeNull(); fireEvent.keyDown(button,{key:"Escape"}); expect(screen.queryByRole("tooltip")).toBeNull(); await act(() => i18n.changeLanguage("en"));
});
it("does not alter shared workflows outside Settings", () => {
  render(<SettingsActionButton icon={SettingsActionIcon.Delete} presentation={SettingsActionPresentation.Icon}>Delete entry</SettingsActionButton>);
  const button=screen.getByRole("button",{name:"Delete entry"}); expect(button.querySelector("svg")).toBeNull(); expect(button.getAttribute("data-settings-action")).toBeNull(); fireEvent.focus(button); expect(screen.queryByRole("tooltip")).toBeNull();
});
it("disambiguates same-name Projects with original IDs and retains exact supported-row callbacks", () => {
  const row = (id: string, schemaVersion = 1) => create(ResourceSchema,{id,kind:EntityKind.PROJECT,schemaVersion,revision:1n,documentJson:encode({name:"Shared name",repositories:[]})});
  const a=row("original-a"), b=row("original-b"), unsupported=row("unsupported",99), edit=vi.fn(),remove=vi.fn();
  render(<SettingsActionScope><ProjectList resources={[a,b,unsupported]} metadata={new Map()} edit={edit} remove={remove}/></SettingsActionScope>);
  fireEvent.click(screen.getByRole("button",{name:"Edit Shared name · original-b"})); expect(edit).toHaveBeenCalledExactlyOnceWith(b);
  fireEvent.click(screen.getByRole("button",{name:"Delete Shared name · original-a"})); expect(remove).toHaveBeenCalledExactlyOnceWith(a);
  fireEvent.click(screen.getByRole("button",{name:`Delete ${resourceName(unsupported)}`})); expect(remove).toHaveBeenCalledTimes(1);
});
it("preserves unsupported Repository and Agent Worker action guards", () => {
  const edit=vi.fn(),remove=vi.fn(),preview=vi.fn(), row=create(ResourceSchema,{id:"unsupported",schemaVersion:99,documentJson:encode({name:"Future resource"})});
  render(<SettingsActionScope><RepositoryRow row={{...row,kind:EntityKind.REPOSITORY}} edit={edit} remove={remove}/><AgentWorkerRow row={{...row,kind:EntityKind.AGENT}} edit={edit} remove={remove} preview={preview}/></SettingsActionScope>);
  for(const button of screen.getAllByRole("button")) { expect(button).toHaveProperty("disabled",true); fireEvent.click(button); }
  expect(edit).not.toHaveBeenCalled();expect(remove).not.toHaveBeenCalled();expect(preview).not.toHaveBeenCalled();
});

it("replaces an explicitly decorative text prefix visually while retaining its original accessible label", () => {
  render(<SettingsActionScope><SettingsActionButton icon={SettingsActionIcon.Add} decorativePrefix="+ ">+ Add account source</SettingsActionButton></SettingsActionScope>);
  const button = screen.getByRole("button", { name: "+ Add account source" }); expect(button.textContent).toBe("Add account source"); expect(button.querySelectorAll("svg")).toHaveLength(1);
});
