// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, InstallationService, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { AppInformation, AppInformationLink, AppInformationPresentationProvider, AppInformationUpdates } from "./app-information";
import { SettingsCategory } from "./settings-category";
import { settingsCategories, settingsGroups } from "./settings";
import { settingsSearchTargets, SettingsSearchTarget, SettingsSearchFocus } from "./settings-search";
import { applicationCommands } from "./command-menu";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { NativeUpdateAction, NativeUpdatePhase } from "./updates";
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => false, invoke: vi.fn() }));
afterEach(async () => { cleanup(); await i18n.changeLanguage("en"); });

it.each(["darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64", "linux-amd64", "linux-arm64"])("reads the actual app context for %s without a server transport", async target => {
  const read = vi.fn(async () => ({ current_version: "0.9.8", target }));
  render(<AppInformation readContext={read} />);
  await screen.findByText("0.9.8");
  expect(screen.getByText(/macOS|Windows|Linux/)).toBeTruthy();
  expect(read).toHaveBeenCalledTimes(1);
  expect(screen.queryByText("0.1.0")).toBeNull();
});
it("announces loading, rejected native context and browser unavailability truthfully", async () => {
  let reject!: (error: unknown) => void;
  const pending = new Promise<{ current_version: string; target: string }>((_, failure) => { reject = failure; });
  const view = render(<AppInformation readContext={() => pending} />);
  expect(screen.getAllByText("Loading app information…").length).toBeGreaterThan(0);
  await act(async () => reject(new Error("invalid-evidence")));
  expect((await screen.findByRole("alert")).textContent).toBe("App information could not be read.");
  view.unmount();
  render(<AppInformation />);
  expect(screen.getAllByText("App information is unavailable.").length).toBeGreaterThan(0);
  expect((screen.getByRole("link", { name: "License" }) as HTMLButtonElement).disabled).toBe(true);
});
it.each(["en", "ko"])("adds the last System category and all static command/search targets in %s", async language => {
  await i18n.changeLanguage(language);
  expect(settingsGroups.at(-1)?.categories.at(-1)).toBe(SettingsCategory.AppInformation);
  expect(settingsCategories[SettingsCategory.AppInformation].label).toBe(language === "en" ? "App information" : "앱 정보");
  const targets = settingsSearchTargets[SettingsCategory.AppInformation]!.map(value => value.target);
  expect(targets).toEqual([SettingsSearchTarget.AppInformation, SettingsSearchTarget.AppUpdates, SettingsSearchTarget.UpdateRecovery, SettingsSearchTarget.AppReleases, SettingsSearchTarget.AppLicense, SettingsSearchTarget.AppNotices]);
  const openSettings = vi.fn();
  const commands = applicationCommands({ navigate: vi.fn(), navigateHeader: vi.fn(), openSettings, newSession: vi.fn(), newGeneralChat: vi.fn(), newProject: vi.fn(), help: vi.fn() });
  commands.find(command => command.value === `settings:${SettingsCategory.AppInformation}:${SettingsSearchTarget.AppUpdates}`)!.run();
  expect(openSettings).toHaveBeenCalledWith(expect.objectContaining({ category: SettingsCategory.AppInformation, target: SettingsSearchTarget.AppUpdates }));
});
it("opens only closed link actions and never retries a failed opener automatically", async () => {
  const open = vi.fn(async (_action: AppInformationLink) => {});
  open.mockRejectedValueOnce(new Error("stopped"));
  render(<AppInformation openLink={open} />);
  fireEvent.click(screen.getByRole("link", { name: "Official releases" }));
  await screen.findByRole("alert");
  expect(open).toHaveBeenCalledExactlyOnceWith(AppInformationLink.Releases);
  fireEvent.click(screen.getByRole("link", { name: "License" }), { detail: 0 });
  await waitFor(() => expect(open).toHaveBeenCalledTimes(2));
  await waitFor(() => expect((screen.getByRole("link", { name: "Open-source notices" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("link", { name: "Open-source notices" }));
  await waitFor(() => expect(open).toHaveBeenCalledTimes(3));
  expect(open.mock.calls.map(([action]) => action)).toEqual([AppInformationLink.Releases, AppInformationLink.License, AppInformationLink.Notices]);
  expect(screen.getByText(/not a complete inventory/)).toBeTruthy();
});

function updaterFixture(candidate = true, supported = true) {
  const id = newRequestId(), revision = 9007199254740993n;
  const resource = create(ResourceSchema, { id, revision, kind: EntityKind.UPDATE, schemaVersion: 1, documentJson: encode({ state: "OBSERVED", version: "0.2.0", target: "darwin-arm64" }) });
  const control = vi.fn(async (action: NativeUpdateAction, _id: string, _revision: bigint) => {
    if (action === NativeUpdateAction.Install) throw new Error("Lost original installation response");
    return { operation_id: id, release_version: "0.2.0", phase: action === NativeUpdateAction.Inspect ? NativeUpdatePhase.Uncertain : NativeUpdatePhase.Prepared };
  });
  const check = vi.fn((_request: unknown) => ({ update: candidate ? resource : undefined }));
  const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: () => ({ capabilities: supported ? [SystemCapability.SIGNED_UPDATES_V1] : [] }) }); router.service(InstallationService, { checkUpdate: check, getUpdate: () => ({ update: resource }) }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const readContext = vi.fn(async () => ({ current_version: "0.1.0", target: "darwin-arm64" }));
  const controls = { readContext, control };
  function Visit() {
    const root = useRef<HTMLDivElement>(null);
    const request = useRef({ category: SettingsCategory.AppInformation, target: SettingsSearchTarget.AppUpdates, generation: newRequestId() }).current;
    return <div ref={root}><h1>Settings fixture</h1><AppInformation readContext={readContext} /><SettingsSearchFocus request={request} category={SettingsCategory.AppInformation} root={root} /></div>;
  }
  const tree = (visit = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><AppInformationPresentationProvider>{visit ? <Visit /> : <p>Other category</p>}<AppInformationUpdates controls={controls} /></AppInformationPresentationProvider></MutationIntents></QueryClientProvider></TransportProvider>;
  return { tree, control, check, id, revision };
}
it("keeps category entry read-only and distinguishes unchecked from a completed no-candidate check", async () => {
  const f = updaterFixture(false);
  render(f.tree());
  const check = await screen.findByRole<HTMLButtonElement>("button", { name: "Check for updates" });
  await waitFor(() => expect(check.disabled).toBe(false));
  expect(screen.getByText("Updates have not been checked.")).toBeTruthy();
  await waitFor(() => expect(document.activeElement?.getAttribute("data-settings-search-target")).toBe("app-updates"));
  expect(f.check).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled();
  fireEvent.click(check);
  await screen.findByText("No newer signed update was found by the completed check.");
  expect(f.check).toHaveBeenCalledTimes(1);
});
it("retains updater DOM, recovery drafts and uncertain install across Settings departure and locale changes", async () => {
  const f = updaterFixture(), view = render(f.tree());
  const check = await screen.findByRole<HTMLButtonElement>("button", { name: "Check for updates" });
  await waitFor(() => expect(check.disabled).toBe(false));
  const recovery = screen.getByText("Update recovery").closest("details")!;
  expect(recovery.open).toBe(false);
  fireEvent.click(check);
  fireEvent.click(await screen.findByRole("button", { name: "Download verified desktop update" }));
  fireEvent.click(await screen.findByRole("button", { name: "Review and install desktop update" }));
  await screen.findByText(/original installation response is unconfirmed/);
  expect(recovery.open).toBe(true);
  const id = screen.getByLabelText("Retained desktop update ID") as HTMLInputElement;
  const revision = screen.getByLabelText("Retained desktop update revision") as HTMLInputElement;
  fireEvent.change(id, { target: { value: f.id } }); fireEvent.change(revision, { target: { value: f.revision.toString() } });
  view.rerender(f.tree(false)); view.rerender(f.tree());
  await waitFor(() => expect(screen.getByLabelText("Retained desktop update ID")).toBe(id));
  expect(revision.value).toBe("9007199254740993");
  await act(() => i18n.changeLanguage("ko"));
  await act(() => i18n.changeLanguage("en"));
  expect(screen.queryByRole("button", { name: "Review and install desktop update" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Inspect retained local installation" }));
  await waitFor(() => expect(f.control).toHaveBeenCalledWith(NativeUpdateAction.Inspect, f.id, f.revision));
  expect(f.control.mock.calls.filter(([action]) => action === NativeUpdateAction.Install)).toHaveLength(1);
  expect(f.check).toHaveBeenCalledTimes(1);
  id.focus(); recovery.open = false;
  expect(document.activeElement).toBe(recovery.querySelector("summary"));
});

it("retries only the original uncertain check and announces failure without claiming a candidate", async () => {
  const f = updaterFixture(false);
  f.check.mockImplementationOnce(() => { throw new ConnectError("Lost check response", Code.Unavailable); });
  render(f.tree());
  const check = await screen.findByRole<HTMLButtonElement>("button", { name: "Check for updates" });
  await waitFor(() => expect(check.disabled).toBe(false));
  fireEvent.click(check);
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same update check" }));
  await screen.findByText("No newer signed update was found by the completed check.");
  expect(f.check).toHaveBeenCalledTimes(2);
  expect(f.check.mock.calls[1][0]).toEqual(f.check.mock.calls[0][0]);
  expect(f.control).not.toHaveBeenCalled();
});
it("retains exact local recovery without signed support and disables malformed original IDs", async () => {
  const f = updaterFixture(false, false);
  render(f.tree());
  await screen.findByText("This server or Worker requires an update to support signed updates.");
  const recovery = screen.getByText("Update recovery").closest("details")!;
  act(() => { recovery.open = true; });
  const id = screen.getByLabelText("Retained desktop update ID");
  const revision = screen.getByLabelText("Retained desktop update revision");
  fireEvent.change(id, { target: { value: "invalid-id" } });
  fireEvent.change(revision, { target: { value: "9007199254740993" } });
  expect((screen.getByRole("button", { name: "Inspect retained local installation" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(id, { target: { value: f.id } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect retained local installation" }));
  await waitFor(() => expect(f.control).toHaveBeenCalledExactlyOnceWith(NativeUpdateAction.Inspect, f.id, 9007199254740993n));
  expect(f.check).not.toHaveBeenCalled();
});
