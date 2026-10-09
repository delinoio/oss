// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState, type ReactNode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ConfigurationService, EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskActions, SettingsTaskDismissButton, SettingsTaskBackground, SettingsTaskDialog, SettingsTasks } from "./settings-task";
import { ConfigurationEditor } from "./settings";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { ProjectEditTab, ProjectEditTabs } from "./project-edit-tabs";
import { readFileSync } from "node:fs";
const projectTabStyles = readFileSync("src/project-edit-tabs.css", "utf8");

function renderTask(children: ReactNode) {
  const transport = createRouterTransport(() => {});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<TransportProvider transport={transport}><QueryClientProvider client={client}>{children}</QueryClientProvider></TransportProvider>);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, failed) => { resolve = done; reject = failed; });
  return { promise, resolve, reject };
}
function fixture() {
  const saved = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.PROJECT, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Saved project" }) });
  const repository = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.REPOSITORY, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Task repository" }) });
  const save = vi.fn(async (_request: unknown) => ({ resource: saved }));
  const transport = createRouterTransport(router => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { listResources: () => ({ resources: [repository] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  function Harness() {
    const [task, setTask] = useState(false);
    return <SettingsTasks><SettingsTaskBackground><button onClick={() => setTask(true)}>New project task</button><p>Mounted project inventory</p></SettingsTaskBackground>
      <button type="button">Independent destination</button>
      {task ? <SettingsTaskDialog title="New Project" size={SettingsDialogSize.Form} close={() => setTask(false)}><ConfigurationEditor kind={EntityKind.PROJECT} active saved={() => setTask(false)} cancel={() => setTask(false)} /></SettingsTaskDialog> : null}
    </SettingsTasks>;
  }
  render(<StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Harness /></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>);
  return { save, saved };
}
async function configure() {
  const checkbox = await screen.findByRole("checkbox", { name: "Task repository" });
  fireEvent.click(checkbox); fireEvent.click(screen.getByRole("button", { name: "Next" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Primary repository" }), { target: { value: (screen.getByRole("option", { name: "Task repository" }) as HTMLOptionElement).value } });
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
}
function submit() {
  const button = screen.getByRole("button", { name: "Save Project" }) as HTMLButtonElement;
  expect(button.form).not.toBeNull();
  fireEvent.submit(button.form!);
}
function escape() {
  const dialog = screen.getByRole("dialog") as HTMLDialogElement;
  fireEvent(dialog, new Event("cancel", { cancelable: true }));
}

it("keeps the inventory mounted, moves actions outside the scrolling body and discards an idle draft", async () => {
  const { save } = fixture(), inventory = screen.getByText("Mounted project inventory");
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  const dialog = screen.getByRole("dialog");
  expect(inventory.closest("[hidden]")).toBeNull();
  expect(inventory.closest("fieldset")?.hasAttribute("inert")).toBe(true);
  expect((inventory.closest("fieldset") as HTMLFieldSetElement).disabled).toBe(true);
  expect(inventory.isConnected).toBe(true);
  expect(inventory.closest("fieldset")?.getAttribute("aria-hidden")).toBe("true");
  const name = screen.getByRole("searchbox", { name: "Search repository names" });
  await waitFor(() => expect(document.activeElement).toBe(name));
  fireEvent.change(name, { target: { value: "Discard this draft" } });
  const saveButton = screen.getByRole("button", { name: "Next" });
  expect(saveButton.closest(".settings-task-footer")).not.toBeNull();
  expect(saveButton.closest(".settings-task-body")).toBeNull();
  fireEvent.click(dialog); // Backdrop/body clicks do not dismiss the task.
  expect(screen.getByRole("dialog")).toBe(dialog);
  escape();
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "New project task" })));
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe("");
  expect(save).not.toHaveBeenCalled();
});

it("discards an uncertain task and permits a fresh request without replaying it", async () => {
  const { save } = fixture();
  save.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  await configure();
  submit();
  await screen.findByRole("button", { name: "Retry the same configuration" });
  const request = save.mock.calls[0][0];
  escape();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("button", { name: "New project task" }).matches(":disabled")).toBe(false);
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  expect((screen.getByRole("searchbox", { name: "Search repository names" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  await configure();
  submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).not.toEqual(request);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("retries the exact uncertain request while its task remains open", async () => {
  const { save } = fixture();
  save.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  await configure();
  submit();
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).toEqual(save.mock.calls[0][0]);
});

it.each(["success", "failure"] as const)("ignores a late %s after close without reopening or taking focus", async result => {
  const { save, saved } = fixture(), pending = deferred<{ resource: typeof saved }>();
  save.mockReturnValue(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  await configure();
  submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  escape();
  const destination = screen.getByRole("button", { name: "Independent destination" });
  destination.focus();
  await act(async () => result === "success" ? pending.resolve({ resource: saved }) : pending.reject(new ConnectError("Lost response", Code.Unavailable)));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(document.activeElement).toBe(destination);
  expect(save).toHaveBeenCalledTimes(1);
});

it("restores opener focus when a successful save closes the task programmatically", async () => {
  fixture();
  const opener = screen.getByRole("button", { name: "New project task" });
  fireEvent.click(opener);
  await configure();
  submit();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(opener));
});

it("uses one native dialog for a confirmation step and restores its original body without remounting", async () => {
  let mounts = 0;
  function Body() {
    const [identity] = useState(() => ++mounts), [confirm, setConfirm] = useState(false);
    return <><p>Original body {identity}</p><button onClick={() => setConfirm(true)}>Delete profile</button>{confirm ? <SettingsTaskDialog title="Confirm profile deletion" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setConfirm(false)}><p>Exact deletion confirmation</p><button type="button" onClick={() => setConfirm(false)}>Keep profile</button></SettingsTaskDialog> : null}</>;
  }
  renderTask(<SettingsTasks><SettingsTaskDialog title="Manage profile" size={SettingsDialogSize.Wide} close={() => {}}><Body /></SettingsTaskDialog></SettingsTasks>);
  fireEvent.click(screen.getByRole("button", { name: "Delete profile" }));
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(screen.getByRole("dialog").getAttribute("data-size")).toBe("confirmation");
  expect(screen.queryByRole("button", { name: "Delete profile" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Keep profile" }));
  expect(screen.getByRole("dialog").getAttribute("data-size")).toBe("wide");
  expect(screen.getByRole("button", { name: "Delete profile" })).toBeTruthy();
  expect(mounts).toBe(1);
});

it("focuses Keep before a destructive action and permits local cancellation during a request", async () => {
  const canceled = vi.fn();
  function Harness() {
    const [open, setOpen] = useState(false), [retained, setRetained] = useState(false);
    return <SettingsTasks><SettingsTaskBackground><button onClick={() => setOpen(true)}>Delete fixture</button></SettingsTaskBackground>
      {open ? <SettingsTaskDialog title="Confirm fixture deletion" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setOpen(false)}>
        <SettingsTaskActions><button onClick={() => setRetained(true)}>Confirm deletion</button><button data-settings-task-cancel disabled={retained} onClick={() => { canceled(); setOpen(false); }}>Keep fixture</button></SettingsTaskActions>
      </SettingsTaskDialog> : null}
    </SettingsTasks>;
  }
  renderTask(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Delete fixture" }));
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Keep fixture" })));
  fireEvent.click(screen.getByRole("button", { name: "Keep fixture" }));
  expect(canceled).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Delete fixture" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm deletion" }));
  fireEvent.click(screen.getByRole("button", { name: "Keep fixture" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(canceled).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("button", { name: "Delete fixture" }).matches(":disabled")).toBe(false);
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
});

it("keeps a fresh task independent of its predecessor's late accepted save", async () => {
  const { save, saved } = fixture(), pending = deferred<{ resource: typeof saved }>();
  save.mockReturnValueOnce(pending.promise);
  const opener = screen.getByRole("button", { name: "New project task" });
  fireEvent.click(opener); await configure(); submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  escape();
  fireEvent.click(opener);
  await configure();
  await act(async () => pending.resolve({ resource: saved }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(save).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).not.toEqual(save.mock.calls[0][0]);
});


it("focuses header close after an audited duplicate is removed, without admitting destruction", async () => {
  const destroy = vi.fn();
  renderTask(<SettingsTaskDialog title="Delete safe fixture" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Close} close={() => {}}><SettingsTaskActions><button onClick={destroy}>Confirm irreversible deletion</button><SettingsTaskDismissButton data-settings-task-cancel>Keep duplicate</SettingsTaskDismissButton></SettingsTaskActions></SettingsTaskDialog>);
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Close Delete safe fixture" })));
  expect(screen.queryByRole("button", { name: "Keep duplicate" })).toBeNull();
  fireEvent.keyDown(document.activeElement!, {key:"Enter"});
  expect(destroy).not.toHaveBeenCalled();
});
it("retains page cancellation and distinct nested return, while omitting empty task actions", async () => {
  const cancel = vi.fn();
  const page = renderTask(<SettingsTaskActions><SettingsTaskDismissButton onClick={cancel}>Cancel page edit</SettingsTaskDismissButton></SettingsTaskActions>);
  fireEvent.click(screen.getByRole("button", { name: "Cancel page edit" })); expect(cancel).toHaveBeenCalledOnce(); page.unmount();
  function Steps() { const [step,setStep] = useState(false); return <><button onClick={() => setStep(true)}>Open nested confirmation</button><SettingsTaskActions><SettingsTaskDismissButton>Cancel outer task</SettingsTaskDismissButton></SettingsTaskActions>{step ? <SettingsTaskDialog title="Nested confirmation" size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setStep(false)}><SettingsTaskActions><button>Confirm nested deletion</button><SettingsTaskDismissButton data-settings-task-cancel onClick={() => setStep(false)}>Keep nested item</SettingsTaskDismissButton></SettingsTaskActions></SettingsTaskDialog> : null}</>; }
  renderTask(<SettingsTaskDialog title="Outer task" size={SettingsDialogSize.Form} close={() => {}}><Steps /></SettingsTaskDialog>);
  expect(screen.queryByRole("button", {name:"Cancel outer task"})).toBeNull(); expect(document.querySelector(".settings-task-footer .actions")).toBeNull();
  fireEvent.click(screen.getByRole("button", {name:"Open nested confirmation"}));
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", {name:"Keep nested item"})));
  fireEvent.click(screen.getByRole("button", {name:"Keep nested item"})); expect(screen.getByRole("dialog", {name:"Outer task"})).toBeTruthy(); expect(screen.queryByRole("button", {name:"Confirm nested deletion"})).toBeNull();
});


it("constrains the saved Project tab body through the mounted task wrapper", () => {
  renderTask(<SettingsTaskDialog title="Edit project" close={() => {}}>
    <style>{projectTabStyles}</style>
    <form className="project-editor"><ProjectEditTabs panels={{
      [ProjectEditTab.General]: <label>Name<input defaultValue="Saved project" /></label>,
      [ProjectEditTab.Repositories]: <p>Repositories</p>,
      [ProjectEditTab.Execution]: <p>Execution controls</p>,
      [ProjectEditTab.Access]: <p>Access</p>,
    }} /><SettingsTaskActions><button type="submit">Save Project</button></SettingsTaskActions></form>
  </SettingsTaskDialog>);
  expect(projectTabStyles).toContain(".settings-task-body");
  const dialog = screen.getByRole("dialog"), form = dialog.querySelector<HTMLFormElement>(".project-editor")!;
  const wrapper = form.parentElement!, body = wrapper.parentElement!;
  expect(body.classList.contains("settings-task-body")).toBe(true);
  // jsdom checks the actual selector/DOM chain, not viewport geometry.
  for (const element of [body, wrapper, form]) {
    expect(getComputedStyle(element).display, element.tagName + ":" + element.className).toBe("flex");
    expect(getComputedStyle(element).minHeight).toBe("0");
  }
  expect(getComputedStyle(body).overflow).toBe("hidden");
  const panels = [...form.querySelectorAll<HTMLElement>('[role="tabpanel"]')];
  expect(panels).toHaveLength(4);
  for (const panel of panels) expect(getComputedStyle(panel).overflowY).toBe("auto");
  expect(screen.getByRole("button", { name: "Save Project" }).closest(".settings-task-body")).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "Execution" }));
  expect(screen.getByRole("tabpanel")).toBe(panels[2]);
  // Internal task steps hide this retained wrapper; the flex rule must not undo it.
  wrapper.hidden = true;
  expect(getComputedStyle(wrapper).display).toBe("none");
});
