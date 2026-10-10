// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { RunnerRemediationProvider, useRunnerRemediation } from "./runner-remediation";
import { useCallback, useState } from "react";
import { useMachineSettingsController } from "./machine-settings";
import { SettingsLifetime } from "./settings-lifetime";
import { MutationIntents } from "./mutation";
import { ResourceChoice, ResourceSelectionPending } from "./configuration-fields";
const machine = (name: string) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name, disabled: false, installations: [{ harness: "claude-code", state: "missing", explicit_path: "", problem: { message: "/private/native secret", guidance: "secret native instruction" } }] }) });
it("retains one inspection draft and original uncertain request across presentation close, without replacing its Runner", async () => {
  const first = machine("First Runner"), second = machine("Second Runner");
  const discover = vi.fn((_request: unknown) => { throw new ConnectError("unavailable", Code.Unavailable); });
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: request => ({ resource: request.id === first.id ? first : second }) }); router.service(WorkerService, { discoverHarnesses: discover }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const open = useRunnerRemediation(); return <><button onClick={() => open?.(first)}>Inspect first</button><button onClick={() => open?.(second)}>Inspect second</button><button disabled={open?.pendingFor(first.id)}>Start original workflow</button>{open?.body}</>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByText("Inspect first"));
  await screen.findByText("First Runner");
  expect(screen.queryByText(/private\/native/)).toBeNull();
  expect(discover).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  const path = screen.getByLabelText("claude-code executable path"); fireEvent.change(path, { target: { value: "/chosen/claude" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  expect((screen.getByRole("button", { name: "Start original workflow" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByText("Inspect second")); expect(screen.queryByText("Second Runner")).toBeNull();
  fireEvent.click(screen.getByText("Inspect first"));
  await waitFor(() => expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/chosen/claude"));
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await screen.findByRole("button", { name: "Retry the same harness check" });
  expect(discover).toHaveBeenCalledOnce(); const request = discover.mock.calls[0][0] as { mutation: { expectedRevision: bigint } };
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  fireEvent.click(screen.getByText("Inspect first"));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same harness check" }));
  await waitFor(() => expect(discover).toHaveBeenCalledTimes(2));
  expect(discover.mock.calls[1][0]).toEqual(request);
  expect(request.mutation.expectedRevision).toBe(9007199254740993n);
  client.clear();
});
it("preserves Worker updates and Network settings in the original full Runner inspection", async () => {
  const row = machine("Settings Runner");
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: () => ({ resource: row }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const open = useRunnerRemediation({ compact: false }); return <><button onClick={() => open?.(row)}>Inspect saved Runner</button>{open?.body}</>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByText("Inspect saved Runner"));
  expect(await screen.findByText("Worker updates")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Network settings" })).toBeTruthy();
  client.clear();
});
it("uses one presenter and preserves the original draft after its calling task disappears", async () => {
  const row = machine("Borrowed Runner");
  const reads = vi.fn(() => ({ resource: row }));
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: reads }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface({ label, active = true }: { label: string; active?: boolean }) { const open = useRunnerRemediation({ active }); return <><button onClick={() => open?.(row)}>{label}</button>{open?.body}</>; }
  const view = (first: boolean, secondActive = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active>{first ? <Surface key="first" label="First caller" /> : null}<Surface key="second" label="Second caller" active={secondActive} /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>;
  const rendered = render(view(true)); fireEvent.click(screen.getByText("First caller"));
  await screen.findByText("Borrowed Runner"); fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  fireEvent.change(screen.getByLabelText("claude-code executable path"), { target: { value: "/original/draft" } });
  fireEvent.click(screen.getByText("Second caller"));
  await waitFor(() => expect(screen.getAllByRole("dialog")).toHaveLength(1));
  rendered.rerender(view(false));
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/original/draft");
  rendered.rerender(view(false, false)); expect(screen.queryByRole("dialog")).toBeNull();
  rendered.rerender(view(false, true)); expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByText("Second caller"));
  await waitFor(() => expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/original/draft"));
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  const before = reads.mock.calls.length;
  await new Promise(resolve => setTimeout(resolve, 10)); expect(reads.mock.calls.length).toBe(before);
  fireEvent.click(screen.getByText("Second caller"));
  await waitFor(() => expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/original/draft"));
  rendered.unmount(); client.clear();
});

it("inspects a newly paired Go Runner with null installation evidence using its exact original revision", async () => {
  const row = create(ResourceSchema, { ...machine("Unobserved Runner"), documentJson: encode({ name: "Unobserved Runner", disabled: false, installations: null }) });
  const discover = vi.fn((_request: unknown) => { throw new ConnectError("unavailable", Code.Unavailable); });
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { getResource: () => ({ resource: row }) });
    router.service(WorkerService, { discoverHarnesses: discover });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const open = useRunnerRemediation(); return <><button onClick={() => open?.(row)}>Inspect unobserved Runner</button>{open?.body}</>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByText("Inspect unobserved Runner"));
  fireEvent.click(await screen.findByRole("button", { name: "Edit executable paths" }));
  expect(discover).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("claude-code executable path"), { target: { value: "/explicit/claude" } });
  fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await screen.findByRole("button", { name: "Retry the same harness check" });
  expect(discover).toHaveBeenCalledOnce();
  expect(discover.mock.calls[0][0]).toMatchObject({ mutation: { id: row.id, expectedRevision: 9007199254740993n } });
  client.clear();
});

it("retains consuming gates for an original diagnostic after selector shortcuts are removed", async () => {
  const row = machine("Original gated Runner");
  const discover = vi.fn((_request: unknown) => { throw new ConnectError("Original lost receipt", Code.Unavailable); });
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: () => ({ resource: row }), listResources: () => ({ resources: [row] }) }); router.service(WorkerService, { discoverHarnesses: discover }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() {
    const inspection = useRunnerRemediation(); const [pending, setPending] = useState(new Map<string, boolean>());
    const report = useCallback((id: string, value: boolean) => setPending(previous => previous.get(id) === value ? previous : new Map(previous).set(id, value)), []);
    return <><ResourceSelectionPending.Provider value={report}><ResourceChoice label="Runner" kind={EntityKind.MACHINE} value={row.id} active change={() => {}} /></ResourceSelectionPending.Provider><button disabled={[...pending.values()].some(Boolean)}>Save consuming workflow</button><button onClick={() => inspection?.(row)}>Open explicit Runner diagnostics</button>{inspection?.body}</>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  expect(screen.queryByRole("button", { name: "Inspect this Runner" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Open explicit Runner diagnostics" }));
  fireEvent.click(await screen.findByRole("button", { name: "Edit executable paths" }));
  fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await screen.findByRole("button", { name: "Retry the same harness check" });
  await waitFor(() => expect(screen.getByRole("button", { name: "Save consuming workflow" })).toHaveProperty("disabled", true));
  const original = discover.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  expect(screen.queryByRole("dialog")).toBeNull(); expect(screen.getByRole("button", { name: "Save consuming workflow" })).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByRole("button", { name: "Open explicit Runner diagnostics" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same harness check" }));
  await waitFor(() => expect(discover).toHaveBeenCalledTimes(2)); expect(discover.mock.calls[1][0]).toEqual(original);
  client.clear();
});

it.each(["succeeded", "failed", "canceled"])("releases consuming Schedule controls after original %s inspection without Finish, retaining results", async state => {
  const row = machine("Original Schedule Runner"), other = machine("Different Runner");
  const initialJob = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.JOB, schemaVersion: 1, revision: 1n, documentJson: encode({ state: "queued" }) });
  let observedJob = initialJob;
  const discover = vi.fn(() => ({ machine: row, job: initialJob }));
  const reads = vi.fn((request: { id: string }) => ({ resource: request.id === initialJob.id ? observedJob : request.id === row.id ? row : other }));
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: reads }); router.service(WorkerService, { discoverHarnesses: discover }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const open = useRunnerRemediation(); return <><button onClick={() => open?.(row)}>Inspect Schedule Runner</button><button onClick={() => open?.(other)}>Inspect other Runner</button><fieldset disabled={open?.pendingFor(row.id)}><input aria-label="Schedule Task" defaultValue="Original schedule draft"/><button>Cancel Schedule</button><button>Next Schedule</button></fieldset>{open?.body}</>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByText("Inspect Schedule Runner"));fireEvent.click(await screen.findByRole("button",{name:"Edit executable paths"}));
  fireEvent.change(screen.getByLabelText("claude-code executable path"), {target:{value:"/submitted/claude"}});
  fireEvent.click(screen.getByRole("button",{name:"Run optional diagnostics"}));await waitFor(()=>expect(discover).toHaveBeenCalledOnce());
  await waitFor(()=>expect(screen.getByRole("button",{name:"Next Schedule"}).matches(":disabled")).toBe(true));
  expect(reads.mock.calls.filter(([request])=>request.id===initialJob.id)).toHaveLength(1);
  observedJob=create(ResourceSchema,{...initialJob,revision:2n,documentJson:encode({state,output:{result:"retained"}})});
  await act(async()=>{await client.invalidateQueries()});await screen.findByRole("button",{name:"Finish inspection"});
  await waitFor(()=>expect(screen.getByRole("button",{name:"Next Schedule"}).matches(":disabled")).toBe(false));
  fireEvent.click(screen.getByRole("button",{name:"Close Inspect installed harnesses"}));
  expect(screen.getByRole("textbox",{name:"Schedule Task"})).toHaveProperty("value","Original schedule draft");
  expect(screen.getByRole("button",{name:"Cancel Schedule"}).matches(":disabled")).toBe(false);
  fireEvent.click(screen.getByText("Inspect Schedule Runner"));expect(await screen.findByRole("button",{name:"Finish inspection"})).toBeTruthy();expect(discover).toHaveBeenCalledOnce();
  client.clear();
});

// The controller must preserve fresh drafts even if another mounted owner updates
// them while the submitted original inspection is still pending.
it("settles only the original submitted paths and preserves fresh Verify and path edits", async () => {
 const row=machine("Draft Runner"), accepted=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.JOB,schemaVersion:1,revision:1n,documentJson:encode({state:"queued"})});
 let job=accepted;
 const discover=vi.fn(()=>({machine:row,job:accepted}));
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:request=>({resource:request.id===accepted.id?job:row})});router.service(WorkerService,{discoverHarnesses:discover})});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 let controller: import("./machine-settings").MachineSettingsController;
 function Surface(){controller=useMachineSettingsController(row,true);return <span>{controller.locked?"Original gate held":"Original gate released"}</span>}
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><SettingsLifetime>{()=> <MutationIntents><Surface/></MutationIntents>}</SettingsLifetime></QueryClientProvider></TransportProvider>);
 act(()=>controller.setEdit({revision:row.revision,paths:{"claude-code":"/submitted"}}));
 await act(async()=>{await controller.discovery.send({mutation:{id:row.id,expectedRevision:row.revision,requestId:newRequestId()},selectionsJson:encode({executables:[{harness:"claude-code",path:"/submitted"}]})})});
 act(()=>{controller.setEdit({revision:row.revision,paths:{"claude-code":"/unsent"}});controller.setVerify(true)});
 job=create(ResourceSchema,{...accepted,revision:2n,documentJson:encode({state:"succeeded"})});await act(async()=>{await client.invalidateQueries()});
 await waitFor(()=>expect(controller.pending).toBe(false));expect(controller.edit?.paths["claude-code"]).toBe("/unsent");expect(controller.locked).toBe(true);expect(controller.verify).toBe(true);
 act(()=>controller.setJob(undefined));expect(controller.locked).toBe(true);expect(discover).toHaveBeenCalledOnce();client.clear();
});

it.each(["foreign-job", "foreign-machine", "missing", "schema", "regressive", "malformed", "failed-read", "uncertain"])("keeps original consuming gate for %s status evidence", async kind => {
 const row=machine("Protected original Runner"), accepted=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.JOB,schemaVersion:1,revision:2n,documentJson:encode({state:"queued"})});
 const terminal=create(ResourceSchema,{...accepted,revision:3n,documentJson:encode({state:"succeeded"})});
 const candidate=kind==="foreign-job"?create(ResourceSchema,{...terminal,id:newRequestId()}):kind==="foreign-machine"?row:kind==="missing"?undefined:kind==="schema"?create(ResourceSchema,{...terminal,schemaVersion:2}):kind==="regressive"?create(ResourceSchema,{...terminal,revision:1n}):kind==="malformed"?create(ResourceSchema,{...terminal,documentJson:new TextEncoder().encode("invalid")}):kind==="uncertain"?create(ResourceSchema,{...terminal,documentJson:encode({state:"uncertain"})}):terminal;
 const jobRead=vi.fn(()=>{if(kind==="failed-read")throw new ConnectError("Original status unavailable",Code.Unavailable);return {resource:candidate}});
 const discover=vi.fn(()=>({machine:row,job:accepted}));
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:request=>request.id===accepted.id?jobRead():{resource:row}});router.service(WorkerService,{discoverHarnesses:discover})});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 function Surface(){const open=useRunnerRemediation();return <><button onClick={()=>open?.(row)}>Inspect protected Runner</button><button disabled={open?.pendingFor(row.id)}>Continue protected schedule</button>{open?.body}</>}
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface/></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getByText("Inspect protected Runner"));fireEvent.click(await screen.findByRole("button",{name:"Run optional diagnostics"}));
 await waitFor(()=>expect(jobRead).toHaveBeenCalledOnce());await waitFor(()=>expect(screen.getByText("Continue protected schedule")).toHaveProperty("disabled",true));
 expect(screen.queryByRole("button",{name:"Finish inspection"})).toBeNull();fireEvent.click(screen.getByRole("button",{name:"Close Inspect installed harnesses"}));
 expect(screen.getByText("Continue protected schedule")).toHaveProperty("disabled",true);expect(discover).toHaveBeenCalledOnce();client.clear();
});
