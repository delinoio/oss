// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState, type ReactNode } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ConfigurationService, EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskActions, SettingsTaskBackground, SettingsTaskDialog, SettingsTasks } from "./settings-task";
import { ConfigurationEditor } from "./settings";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

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
  const save = vi.fn(async (_request: unknown) => ({ resource: saved }));
  const transport = createRouterTransport(router => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { listResources: () => ({ resources: [] }) });
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
  expect(inventory.isConnected).toBe(true);
  expect(inventory.closest("fieldset")?.getAttribute("aria-hidden")).toBe("true");
  const name = screen.getByRole("textbox", { name: "Name" });
  await waitFor(() => expect(document.activeElement).toBe(name));
  fireEvent.change(name, { target: { value: "Discard this draft" } });
  const saveButton = screen.getByRole("button", { name: "Save Project" });
  expect(saveButton.closest(".settings-task-footer")).not.toBeNull();
  expect(saveButton.closest(".settings-task-body")).toBeNull();
  fireEvent.click(dialog); // Backdrop/body clicks do not dismiss the task.
  expect(screen.getByRole("dialog")).toBe(dialog);
  escape();
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "New project task" })));
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(save).not.toHaveBeenCalled();
});

it("discards an uncertain task and permits a fresh request without replaying it", async () => {
  const { save } = fixture();
  save.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Original submitted project" } });
  submit();
  await screen.findByRole("button", { name: "Retry the same configuration" });
  const request = save.mock.calls[0][0];
  escape();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("button", { name: "New project task" }).matches(":disabled")).toBe(false);
  expect(screen.queryByRole("button", { name: "View original operation" })).toBeNull();
  expect(save).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).not.toEqual(request);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

it("retries the exact uncertain request while its task remains open", async () => {
  const { save } = fixture();
  save.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
  submit();
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same configuration" }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).toEqual(save.mock.calls[0][0]);
});

it.each(["success", "failure"] as const)("ignores a late %s after close without reopening or taking focus", async result => {
  const { save, saved } = fixture(), pending = deferred<{ resource: typeof saved }>();
  save.mockReturnValue(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "New project task" }));
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
  submit();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(document.activeElement).toBe(opener);
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
  fireEvent.click(opener); submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  escape();
  fireEvent.click(opener);
  const name = screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement;
  fireEvent.change(name, { target: { value: "Fresh task" } }); name.focus();
  await act(async () => pending.resolve({ resource: saved }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(name.value).toBe("Fresh task");
  expect(document.activeElement).toBe(name);
  expect(save).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  submit();
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).not.toEqual(save.mock.calls[0][0]);
});
