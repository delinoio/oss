// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EntityKind } from "@delinoio/delidev-api-client";
import { ConfigurationNameField, NameConflictCompletion, configurationNameProblem } from "./configuration-name";
import { JobState } from "./jobs";
import { ProjectEditTabs, ProjectEditTab } from "./project-edit-tabs";
afterEach(cleanup);
const problem = { code: "conflict", cause: "configuration_name_conflict" };
describe("server-owned configuration name conflicts", () => {
 it.each([EntityKind.PROJECT, EntityKind.REPOSITORY])("retains spelling and permits correcting the rejected %s draft", async kind => {
  function Draft() { const [name, change] = useState(" Café "); return <ConfigurationNameField kind={kind} name={name} change={change} conflict={problem} />; }
  render(<Draft />);
  expect(screen.getByRole("alert").textContent).toBe(`A ${kind === EntityKind.PROJECT ? "project" : "repository"} with this name already exists. Choose another name.`);
  const input = screen.getByRole("textbox"); expect((input as HTMLInputElement).value).toBe(" Café "); expect(input.getAttribute("aria-invalid")).toBe("true");
  fireEvent.change(input, { target: { value: "different" } }); expect((input as HTMLInputElement).value).toBe("different"); expect(screen.queryByRole("alert")).toBeNull();
 });
 it("reveals the existing name panel without replacing the edited draft", async () => {
  const panels = { [ProjectEditTab.General]: <ConfigurationNameField kind={EntityKind.PROJECT} name="draft" change={vi.fn()} />, [ProjectEditTab.Repositories]: <p>repos</p>, [ProjectEditTab.Execution]: null, [ProjectEditTab.Access]: null };
  const view = render(<ProjectEditTabs disabled={false} panels={panels} />); fireEvent.click(screen.getByRole("tab", {name:"Repositories"}));
  view.rerender(<ProjectEditTabs disabled={false} panels={{ ...panels, [ProjectEditTab.General]: <ConfigurationNameField kind={EntityKind.PROJECT} name="draft" change={vi.fn()} conflict={problem} /> }} />);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 25)); });
  expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("draft"); expect(document.activeElement).toBe(screen.getByRole("textbox"));
 });
 it("corrects only a definitive typed terminal failure, once", () => {
  const complete = vi.fn(); const view = render(<NameConflictCompletion state={JobState.Uncertain} problem={problem} complete={complete} />); expect(complete).not.toHaveBeenCalled();
  view.rerender(<NameConflictCompletion state={JobState.Failed} problem={{code:"conflict"}} complete={complete} />); expect(complete).not.toHaveBeenCalled();
  view.rerender(<NameConflictCompletion state={JobState.Failed} problem={problem} complete={complete} />); expect(complete).toHaveBeenCalledTimes(1);
  view.rerender(<NameConflictCompletion state={JobState.Failed} problem={{...problem}} complete={complete} />); expect(complete).toHaveBeenCalledTimes(1);
  expect(configurationNameProblem({code:"recovery_required", cause:problem.cause})).toBe(false);
 });
});
