// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { it, expect, vi } from "vitest";
import { AgentPermissions, Harness } from "./configuration-fields";

it("uses exact ordered local choices and keeps navigation separate from edits", () => {
 const change = vi.fn(); render(<AgentPermissions harness={Harness.Codex} options={{ permission: "default", future: "keep" }} change={change} active disabled={false}/>);
 const trigger = screen.getByRole("combobox", { name: "Permission mode" }); trigger.focus(); fireEvent.click(trigger);
 expect(screen.getAllByRole("option").map(row => row.dataset.pickerId)).toEqual(["default", "read-only", "workspace-write", "full-access"]);
 expect(screen.queryByRole("button", { name: /Load more|Retry|Reload/ })).toBeNull();
 fireEvent.keyDown(trigger, { key: "End" }); fireEvent.keyDown(trigger, { key: "Home" }); fireEvent.keyDown(trigger, { key: "ArrowDown" }); expect(change).not.toHaveBeenCalled();
 fireEvent.keyDown(trigger, { key: "Enter" }); expect(change).toHaveBeenCalledExactlyOnceWith({ permission: "read-only", future: "keep" }); expect(document.activeElement).toBe(trigger);
 fireEvent.click(trigger); fireEvent.keyDown(trigger, { key: "Escape" }); expect(screen.queryByRole("listbox")).toBeNull(); expect(document.activeElement).toBe(trigger);
});

it("keeps retained unsupported values until explicit selection and preserves Claude options", () => {
 const change = vi.fn(); render(<AgentPermissions harness={Harness.Claude} options={{ permission: "workspace-write", approval_policy: "on-request", claude_permission: "future-mode", future: "keep" }} change={change} active disabled={false}/>);
 const trigger = screen.getByRole("combobox"); expect(trigger.textContent).toBe("Unsupported selection · future-mode"); expect(change).not.toHaveBeenCalled(); fireEvent.click(trigger);
 expect(screen.getAllByRole("option").map(row => row.dataset.pickerId)).toEqual(["future-mode", "default", "plan", "acceptEdits", "dontAsk", "bypassPermissions", "auto"]);
 fireEvent.click(screen.getByRole("option", { name: "auto" })); expect(change).toHaveBeenCalledExactlyOnceWith({ permission: "workspace-write", approval_policy: "on-request", claude_permission: "auto", future: "keep" });
});

for (const harness of [Harness.OpenCode, Harness.Grok]) it(`${harness} retains default-only eligibility`, () => {
 const change = vi.fn(); render(<AgentPermissions harness={harness} options={{ permission: "default" }} change={change} active disabled={false}/>); fireEvent.click(screen.getByRole("combobox"));
 expect(screen.getAllByRole("option").map(row => row.dataset.pickerId)).toEqual(["default"]); expect(screen.getByText(/This tool uses its native permissions/)).toBeTruthy(); expect(change).not.toHaveBeenCalled();
});

for (const lock of ["inactive", "pending", "fieldset", "hidden", "disposed"]) it(`rejects queued permission choice after ${lock}`, () => {
 const change = vi.fn(), view = (active: boolean, disabled: boolean) => <fieldset data-owner><AgentPermissions harness={Harness.Codex} options={{ permission: "default" }} change={change} active={active} disabled={disabled}/></fieldset>;
 const result = render(view(true, false)); fireEvent.click(screen.getByRole("combobox")); const option = screen.getByRole("option", { name: "full-access" });
 if (lock === "inactive") result.rerender(view(false, false)); else if (lock === "pending") result.rerender(view(true, true)); else if (lock === "disposed") result.unmount(); else if (lock === "fieldset") (result.container.querySelector("fieldset") as HTMLFieldSetElement).disabled = true; else result.container.querySelector("fieldset")!.hidden = true;
 fireEvent.click(option); expect(change).not.toHaveBeenCalled();
});
