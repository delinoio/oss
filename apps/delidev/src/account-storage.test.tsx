// SPDX-License-Identifier: Apache-2.0
import { StrictMode, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { useAccountStorage } from "./account-storage";
import { encode, document, object, text, type Document } from "./documents";
import { i18n } from "./localization";
import { SettingsLifetime } from "./settings-lifetime";

function account(alias: string, type = "api", connected = true) {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 1, documentJson: encode({ alias, type, connection: connected ? { id: newRequestId() } : undefined }) });
}
function observation(row: Resource, state = "observed", code?: string): Document {
  return { account_id: row.id, connection_id: text(object(document(row).connection).id) || undefined, result: { state, code } };
}
function report(credentials: Document[]): Document {
  return { schema_version: 2, server_id: newRequestId(), version: "0.1.0", observed_at: "2026-10-07T01:23:45.123456789Z", credentials, more_credentials: false };
}
function fixture(initial: Document) {
  const state = { report: initial };
  const doctor = vi.fn(async () => ({ reportJson: encode(state.report) }));
  const transport = createRouterTransport(router => router.service(SystemService, { getDoctor: doctor }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (children: ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}>{children}</QueryClientProvider></TransportProvider>;
  return { state, doctor, client, view };
}
function List({ rows, active = true, stale = false }: { rows: Resource[]; active?: boolean; stale?: boolean }) {
  const storage = useAccountStorage(rows, active, stale);
  return <>{storage.header}{rows.map(row => <article key={row.id} aria-label={text(document(row).alias)}>{storage.forAccount(row)}</article>)}</>;
}
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function refresh() {
  fireEvent.click(screen.getByRole("button", { name: "Refresh account storage" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh account storage" })).toHaveProperty("disabled", false));
}

it("shares one read across rows, owns each failure and keeps ordinary states out of alerts", async () => {
  const failed = account("Work key"), keyless = account("Local API"), disconnected = account("Disconnected", "api", false), subscription = account("Personal", "subscription"), readable = account("Readable");
  const rows = [failed, keyless, disconnected, subscription, readable];
  const failure = observation(failed, "failed", "permission_denied"); failure.result = { ...object(failure.result), guidance: '<img src=x onerror="alert(1)">' };
  const value = fixture(report([failure, observation(keyless, "not-applicable"), observation(disconnected, "unconfigured"), observation(subscription, "unavailable", "unsupported"), observation(readable)]));
  const view = render(value.view(<List rows={rows} />));
  await screen.findByText("Protected credential could not be read");
  expect(value.doctor).toHaveBeenCalledTimes(1);
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  const work = screen.getByRole("article", { name: "Work key" });
  expect(within(work).getByRole("alert").textContent).toContain("Do not recreate the saved key.");
  for (const name of ["Local API", "Disconnected", "Personal", "Readable"]) expect(within(screen.getByRole("article", { name })).queryByRole("alert")).toBeNull();
  expect(within(screen.getByRole("article", { name: "Personal" })).getByText(/No sign-in was read or changed/)).toBeTruthy();
  const details = within(work).getByText("Technical details").closest("details")!;
  expect(details.open).toBe(false); expect(details.textContent).toContain("permission_denied"); expect(details.textContent).toContain("2026-10-07T01:23:45.123456789Z");
  expect(details.textContent).toContain(text(object(document(failed).connection).id));
  expect(view.container.querySelector("img")).toBeNull();
});

it("preserves disclosure identity on reorder/refresh but resets it for changed server, connection or account revision", async () => {
  const a = account("A"), b = account("B"), data = report([observation(a, "failed", "permission_denied"), observation(b, "failed", "permission_denied")]), value = fixture(data);
  const view = render(value.view(<List rows={[a, b]} />)); await screen.findAllByText("Protected credential could not be read");
  const details = within(screen.getByRole("article", { name: "A" })).getByText("Technical details").closest("details")!; details.open = true;
  view.rerender(value.view(<List rows={[b, a]} />)); await refresh();
  expect(value.doctor).toHaveBeenCalledTimes(2); expect(within(screen.getByRole("article", { name: "A" })).getByText("Technical details").closest("details")).toBe(details); expect(details.open).toBe(true);
  value.state.report = { ...data, server_id: newRequestId() }; await refresh();
  expect(within(screen.getByRole("article", { name: "A" })).getByText("Technical details").closest("details")!.open).toBe(false);
  const changed = create(ResourceSchema, { ...a, revision: 2n, documentJson: encode({ ...document(a), connection: { id: newRequestId() } }) });
  value.state.report = { ...data, credentials: [observation(changed, "failed", "permission_denied"), observation(b, "failed", "permission_denied")] };
  view.rerender(value.view(<List rows={[changed, b]} />));
  await waitFor(() => expect(value.doctor).toHaveBeenCalledTimes(4));
  await screen.findAllByText("Protected credential could not be read");
  expect(within(screen.getByRole("article", { name: "A" })).getByText("Technical details").closest("details")!.open).toBe(false);
});

it("does not attach a late old-generation response after a connection change", async () => {
  const row = account("Original"), old = report([observation(row)]), value = fixture(old), pending = deferred<{ reportJson: Uint8Array }>();
  value.doctor.mockImplementationOnce(() => pending.promise);
  const view = render(value.view(<List rows={[row]} />)); await waitFor(() => expect(value.doctor).toHaveBeenCalledTimes(1));
  const changed = create(ResourceSchema, { ...row, revision: 2n, documentJson: encode({ ...document(row), connection: { id: newRequestId() } }) });
  value.state.report = report([observation(changed, "failed", "not_found")]);
  view.rerender(value.view(<List rows={[changed]} />)); await screen.findByText("Protected credential could not be read");
  await act(async () => pending.resolve({ reportJson: encode(old) }));
  expect(screen.queryByText("Protected credential was readable")).toBeNull();
  expect(value.doctor).toHaveBeenCalledTimes(2);
});

it("labels retained observations during deferred refresh, failure and stale list reads", async () => {
  const row = account("Account"), value = fixture(report([observation(row, "failed", "permission_denied")]));
  const view = render(value.view(<List rows={[row]} />)); await screen.findByText("Protected credential could not be read");
  const pending = deferred<{ reportJson: Uint8Array }>(); value.doctor.mockImplementationOnce(() => pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Refresh account storage" }));
  await screen.findAllByText(/Showing a previous observation/);
  expect(screen.getByRole("button", { name: "Refresh account storage" })).toHaveProperty("disabled", true);
  await act(async () => pending.reject(new ConnectError("PRIVATE_RAW_ERROR", Code.PermissionDenied)));
  await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(2)); expect(screen.queryByText("PRIVATE_RAW_ERROR")).toBeNull();
  expect(screen.getByText("Protected credential could not be read")).toBeTruthy();
  view.rerender(value.view(<List rows={[row]} stale />));
  expect(screen.getByRole("button", { name: "Refresh account storage" })).toHaveProperty("disabled", true);
});

it("does not infer success for mismatched, superseded or missing/truncated accounts", async () => {
  const row = account("Mismatch"), missing = account("Not inspected"), superseded = account("Changed"), wrong = observation(row); wrong.connection_id = newRequestId();
  const value = fixture({ ...report([wrong, observation(superseded, "superseded", "conflict")]), more_credentials: true });
  render(value.view(<List rows={[row, missing, superseded]} />));
  await screen.findAllByText(/account connection changed during inspection/);
  expect(screen.getByText(/Only the first 50 accounts were inspected/)).toBeTruthy();
  expect(within(screen.getByRole("article", { name: "Not inspected" })).getByText("No matching account storage observation is available.")).toBeTruthy();
  expect(screen.queryByText("Protected credential was readable")).toBeNull(); expect(screen.queryByRole("alert")).toBeNull();
});

for (const kind of ["legacy", "future", "missing", "duplicate", "oversized", "invalid-state", "invalid-guidance", "invalid-time", "null-connection", "invalid-code-state"]) it(`rejects ${kind} report observations without a false successful read`, async () => {
  const row = account("Account"), record = observation(row), data = report([record]);
  if (kind === "legacy") delete data.schema_version;
  if (kind === "future") data.schema_version = 3;
  if (kind === "missing") delete data.credentials;
  if (kind === "duplicate") data.credentials = [record, record];
  if (kind === "oversized") data.credentials = Array.from({ length: 51 }, () => record);
  if (kind === "invalid-state") record.result = { state: "future" };
  if (kind === "invalid-guidance") record.result = { state: "observed", guidance: { secret: "never rendered" } };
  if (kind === "invalid-time") data.observed_at = "2026-02-30T01:23:45Z";
  if (kind === "null-connection") record.connection_id = null;
  if (kind === "invalid-code-state") record.result = { state: "unavailable", code: "permission_denied" };
  const value = fixture(data); render(value.view(<List rows={[row]} />));
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh account storage" })).toHaveProperty("disabled", false));
  expect(screen.queryByText("Protected credential was readable")).toBeNull();
  expect(screen.getByText("No matching account storage observation is available.")).toBeTruthy();
});

it("keeps language/focus/disclosure identity without rereading and clears reader caches on category disposal", async () => {
  const row = account("Account"), value = fixture(report([observation(row, "failed", "permission_denied")]));
  const view = render(value.view(<StrictMode><SettingsLifetime>{() => <List rows={[row]} />}</SettingsLifetime></StrictMode>));
  await screen.findByText("Protected credential could not be read");
  const summary = screen.getByText("Technical details"), details = summary.closest("details")!; details.open = true; summary.focus();
  const count = value.doctor.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  expect(screen.getByText("기술 상세")).toBe(summary); expect(globalThis.document.activeElement).toBe(summary); expect(details.open).toBe(true); expect(value.doctor).toHaveBeenCalledTimes(count);
  view.unmount();
  await waitFor(() => expect(value.client.getQueryCache().getAll()).toHaveLength(0));
});

it("does not read an empty or inactive list and quarantines malformed UTF-8/large responses", async () => {
  const row = account("Account"), value = fixture(report([observation(row)]));
  const view = render(value.view(<List rows={[]} />));
  view.rerender(value.view(<List rows={[row]} active={false} />)); expect(value.doctor).not.toHaveBeenCalled();
  value.doctor.mockResolvedValueOnce({ reportJson: new Uint8Array([0xff]) });
  view.rerender(value.view(<List rows={[row]} />)); await screen.findByText(/report is malformed/);
  value.doctor.mockResolvedValueOnce({ reportJson: new Uint8Array((1 << 20) + 1) }); await refresh();
  expect(screen.queryByText("Protected credential was readable")).toBeNull();
});

it("handles initial denied reads and true/false/unknown inventory completeness independently", async () => {
  const row = account("Account"), value = fixture(report([observation(row)]));
  value.doctor.mockRejectedValueOnce(new ConnectError("PRIVATE_DENIAL", Code.PermissionDenied));
  render(value.view(<List rows={[row]} />)); await screen.findByRole("alert");
  expect(screen.queryByText("Protected credential was readable")).toBeNull();
  expect(screen.queryByText("PRIVATE_DENIAL")).toBeNull();
  await refresh(); expect(screen.queryByText("Protected credential was readable")).toBeNull();
  expect(screen.queryByText(/first 50 accounts/)).toBeNull(); expect(screen.queryByText(/completeness is unknown/)).toBeNull();
  delete value.state.report.more_credentials; await refresh();
  expect(screen.getByText("Account storage inventory completeness is unknown.")).toBeTruthy();
  value.state.report.more_credentials = true; await refresh();
  expect(screen.getByText(/Only the first 50 accounts were inspected/)).toBeTruthy();
});

it("keeps ChatGPT saved storage independent of account health and localizes deferred ownership", async () => {
  const row = create(ResourceSchema, { ...account("ChatGPT", "subscription"), documentJson: encode({ alias: "ChatGPT", type: "subscription", subscription_service: "chatgpt", health: "failed", connection: { id: newRequestId() } }) });
  const value = fixture(report([observation(row, "unavailable", "unavailable")]));
  render(value.view(<List rows={[row]} />));
  await screen.findByText(/Saved ChatGPT storage cannot be inspected/);
  expect(screen.queryByRole("alert")).toBeNull();
  const count = value.doctor.mock.calls.length;
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.getByText(/계정 소유권이 해결되지 않았거나/)).toBeTruthy();
  expect(value.doctor).toHaveBeenCalledTimes(count);
  await act(async () => { await i18n.changeLanguage("en"); });
  value.state.report = report([observation(row, "failed", "permission_denied")]); await refresh();
  expect(screen.getByRole("alert").textContent).toContain("Do not recreate the saved bundle.");
  value.state.report = report([observation(row, "superseded", "conflict")]); await refresh();
  expect(screen.queryByText(/Saved ChatGPT storage cannot be inspected/)).toBeNull();
  expect(screen.getByText(/account connection changed during inspection/)).toBeTruthy();
});

it("hides matching successful API and subscription observations while retaining the shared reader", async () => {
  const api = account("API key"), subscription = account("Subscription", "subscription");
  const value = fixture(report([observation(api), observation(subscription)]));
  render(value.view(<List rows={[api, subscription]} />));
  await waitFor(() => expect(screen.getByRole("button", { name: "Refresh account storage" })).toHaveProperty("disabled", false));
  expect(value.doctor).toHaveBeenCalledTimes(1);
  for (const name of ["API key", "Subscription"]) {
    const row = screen.getByRole("article", { name });
    expect(row.querySelector(".account-storage-notice")).toBeNull();
    expect(within(row).queryByText("Technical details")).toBeNull();
  }
  expect(screen.queryByText("Protected credential was readable")).toBeNull();
  const pending = deferred<{ reportJson: Uint8Array }>();
  value.doctor.mockImplementationOnce(() => pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Refresh account storage" }));
  await screen.findByText(/Showing a previous observation/);
  expect(screen.getByRole("button", { name: "Refresh account storage" })).toHaveProperty("disabled", true);
  await act(async () => pending.reject(new ConnectError("PRIVATE_SUCCESS_REFRESH_ERROR", Code.PermissionDenied)));
  await screen.findByRole("alert");
  expect(screen.queryByText("PRIVATE_SUCCESS_REFRESH_ERROR")).toBeNull();
  expect(screen.queryByText("Protected credential was readable")).toBeNull();
  expect(screen.getByRole("article", { name: "API key" }).querySelector(".account-storage-notice")).toBeNull();
});
