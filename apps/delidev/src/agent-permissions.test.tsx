// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { it, expect, vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
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
 const trigger = screen.getAllByRole("combobox")[0]; expect(trigger.textContent).toBe("Unsupported selection · future-mode"); expect(change).not.toHaveBeenCalled(); fireEvent.click(trigger);
 expect(screen.getAllByRole("option").map(row => row.dataset.pickerId)).toEqual(["future-mode", "default", "plan", "acceptEdits", "dontAsk", "bypassPermissions", "auto"]);
 fireEvent.click(screen.getByRole("option", { name: "auto" })); expect(change).toHaveBeenCalledExactlyOnceWith({ permission: "workspace-write", approval_policy: "on-request", claude_permission: "auto", future: "keep" });
});

for (const harness of [Harness.OpenCode, Harness.Grok]) it(`${harness} retains default-only eligibility`, () => {
 const change = vi.fn(); render(<AgentPermissions harness={harness} options={{ permission: "default" }} change={change} active disabled={false}/>); fireEvent.click(screen.getAllByRole("combobox")[0]);
 expect(screen.getAllByRole("option").map(row => row.dataset.pickerId)).toEqual(["default"]); expect(screen.getByText(/This tool uses its native permissions/)).toBeTruthy(); expect(change).not.toHaveBeenCalled();
});

for (const lock of ["inactive", "pending", "fieldset", "hidden", "disposed"]) it(`rejects queued permission choice after ${lock}`, () => {
 const change = vi.fn(), view = (active: boolean, disabled: boolean) => <fieldset data-owner><AgentPermissions harness={Harness.Codex} options={{ permission: "default" }} change={change} active={active} disabled={disabled}/></fieldset>;
 const result = render(view(true, false)); fireEvent.click(screen.getAllByRole("combobox")[0]); const option = screen.getByRole("option", { name: "full-access" });
 if (lock === "inactive") result.rerender(view(false, false)); else if (lock === "pending") result.rerender(view(true, true)); else if (lock === "disposed") result.unmount(); else if (lock === "fieldset") (result.container.querySelector("fieldset") as HTMLFieldSetElement).disabled = true; else result.container.querySelector("fieldset")!.hidden = true;
 fireEvent.click(option); expect(change).not.toHaveBeenCalled();
});

it("shows the native default for an omitted legacy permission without creating a value", () => {
 const change = vi.fn(); render(<AgentPermissions harness={Harness.Codex} options={{ future: "keep" }} change={change} active disabled={false}/>);
 const trigger = screen.getAllByRole("combobox")[0]; expect(trigger.textContent).toBe("default"); expect(trigger.dataset.value).toBe(""); expect(change).not.toHaveBeenCalled();
});

it("selects native AI review without expanding the sandbox", () => {
 const change = vi.fn(); render(<AgentPermissions harness={Harness.Codex} options={{permission:"read-only",future:"keep"}} change={change} active disabled={false} reviewSupported />);
 fireEvent.click(screen.getByRole("combobox",{name:"Approval review"}));fireEvent.click(screen.getByRole("option",{name:"AI auto-review"}));
 expect(change).toHaveBeenCalledExactlyOnceWith({permission:"read-only",future:"keep",approvals_reviewer:"auto_review",approval_policy:"on-request"});
});
it("retains a foreign reviewer until explicit clearing", () => {
 const change = vi.fn();render(<AgentPermissions harness={Harness.Claude} options={{permission:"default",approvals_reviewer:"auto_review",approval_policy:"on-request"}} change={change} active disabled={false}/>);
 expect(change).not.toHaveBeenCalled();expect(screen.getByText(/A Codex approval reviewer is retained/)).toBeTruthy();fireEvent.click(screen.getByRole("button",{name:"Clear retained option"}));
 expect(change).toHaveBeenCalledExactlyOnceWith({permission:"default",approval_policy:"on-request"});
});
it("does not offer reviewer authority before capability negotiation",()=>{
 const change=vi.fn();render(<AgentPermissions harness={Harness.Codex} options={{permission:"default",approvals_reviewer:"auto_review"}} change={change} active disabled={false}/>);
 expect(screen.getByRole("combobox",{name:"Approval review"}).hasAttribute("disabled")).toBe(true);expect(change).not.toHaveBeenCalled();
});

for (const language of [SupportedLanguage.English, SupportedLanguage.Korean]) {
 it(`keeps Claude first-dispatch guidance conditional in ${language} without granting authority`, async () => {
  await i18n.changeLanguage(language);
  const change=vi.fn();
  const {container}=render(<AgentPermissions harness={Harness.Claude} options={{permission:"default",claude_permission:"default",future:"keep"}} change={change} active disabled={false}/>);
  expect(container.textContent).toContain(language===SupportedLanguage.English ? "Saving settings does not authorize or start Claude execution." : "설정을 저장하는 것만으로 Claude 실행이 허용되거나 시작되지는 않습니다.");
  for(const requirement of language===SupportedLanguage.English ? ["verified selected account or supported subscription", "Anthropic Messages profile", "selected Runner Device", "model/version", "input mode", "continuation history"] : ["검증된 선택 계정 또는 지원되는 구독", "Anthropic Messages 프로필", "선택 실행 장치", "모델과 버전", "입력 모드", "대화 이력"])
   expect(container.textContent).toContain(requirement);
  expect(container.textContent).not.toMatch(/public execution integration is still unavailable|공개 실행 연동은 아직 사용할 수 없습니다/);
  expect(container.textContent).toContain(language===SupportedLanguage.English ? "unverified or unsupported" : "검증되지 않았거나 지원되지 않는");
  expect(change).not.toHaveBeenCalled();
 });
 it(`retains specific incompatible and unsupported Claude guidance in ${language}`, async()=>{
  await i18n.changeLanguage(language);
  const change=vi.fn();render(<AgentPermissions harness={Harness.Claude} options={{permission:"workspace-write",approval_policy:"on-request",claude_permission:"unverified-native-mode",future:"keep"}} change={change} active disabled={false}/>);
  expect(screen.getByRole("alert").textContent).toContain("workspace-write");
  expect(screen.getByRole("alert").textContent).toContain("on-request");
  expect(screen.getByRole("combobox").textContent).toContain("unverified-native-mode");
  expect(change).not.toHaveBeenCalled();
 });
}
