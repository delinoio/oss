import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { EntityKind, ErrorDetailSchema, FailureCode, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Surface } from "./views";
import { Sidebar } from "./sidebar";
import type { ComponentProps } from "react";
import { ServerPresentationKind } from "./server-presentation";
import { i18n, SupportedLanguage } from "./localization";

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

function resource(kind: EntityKind, name: string, projectId = "", values: Record<string, unknown> = {}): Resource {
  const id = newRequestId();
  const row = create(ResourceSchema, { id, sessionId: kind === EntityKind.SESSION ? id : "", projectId, kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name, ...values }) });
  return row;
}

function mountSidebar({ projects, sessions, props = {}, stateful = false }: {
  projects: (pageToken: string) => { resources: Resource[]; nextPageToken?: string } | Promise<{ resources: Resource[]; nextPageToken?: string }>;
  sessions: (request: { projectId: string; includeArchived: boolean; pageToken: string }) => { sessions: Resource[]; nextPageToken?: string } | Promise<{ sessions: Resource[]; nextPageToken?: string }>;
  stateful?: boolean;
  props?: Partial<ComponentProps<typeof Sidebar>>;
}) {
  const projectRequests: string[] = [];
  const sessionRequests: { projectId: string; includeArchived: boolean; pageToken: string }[] = [];
  const transport: Transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }) });
    router.service(ResourceService, { listResources: (request) => {
      if (request.filter?.kind !== EntityKind.PROJECT) return { resources: [] };
      const token = request.filter.pageToken;
      projectRequests.push(token);
      return projects(token);
    } });
    router.service(SessionService, { listSessions: (request) => {
      const value = { projectId: request.projectId, includeArchived: request.includeArchived, pageToken: request.pageToken };
      sessionRequests.push(value);
      return sessions(value);
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0, gcTime: 60000, refetchOnWindowFocus: false } } });
  const openSession = vi.fn();
  const openSettings = vi.fn();
  let currentProps = props;
  let selectSurface!: (surface: Surface) => void;
  const newSession = vi.fn(() => { if (stateful) selectSurface(Surface.NewSession); });
  const navigate = vi.fn((surface: Surface) => { if (stateful) selectSurface(surface); });
  function Harness() {
    const [surface, setSurface] = useState(Surface.Sessions);
    selectSurface = setSurface;
    return <Sidebar surface={surface} selectedSessionId="" navigate={navigate} openSession={openSession} newSession={newSession} openSettings={openSettings} {...currentProps} />;
  }
  const tree = () => <TransportProvider transport={transport}><QueryClientProvider client={client}><Harness /></QueryClientProvider></TransportProvider>;
  const view = render(tree());
  const setProps = (next: Partial<ComponentProps<typeof Sidebar>>) => { currentProps = { ...currentProps, ...next }; view.rerender(tree()); };
  return { ...view, setProps, client, navigate, openSession, openSettings, newSession, projectRequests, sessionRequests, setSurface: (surface: Surface) => act(() => selectSurface(surface)) };
}

it("keeps equal-name projects separate, includes empty projects, and only reads expanded project pages", async () => {
  const first = resource(EntityKind.PROJECT, "Same name");
  const second = resource(EntityKind.PROJECT, "Same name");
  const empty = resource(EntityKind.PROJECT, "Empty project");
  const firstSession = resource(EntityKind.SESSION, "First session", first.id, { workspace: "worktree", outcome: "running", archive: "active" });
  const secondSession = resource(EntityKind.SESSION, "Second session", second.id, { workspace: "local", outcome: "failed", archive: "archiving" });
  const value = mountSidebar({
    projects: () => ({ resources: [first, second, empty] }),
    sessions: (request) => request.projectId === first.id ? { sessions: [firstSession] } : request.projectId === second.id ? { sessions: [secondSession] } : { sessions: [] },
  });

  const firstGroup = await screen.findByRole("button", { name: `Same name. Project ID: ${first.id}` });
  const secondGroup = screen.getByRole("button", { name: `Same name. Project ID: ${second.id}` });
  expect(firstGroup).not.toBe(secondGroup);
  expect(firstGroup.getAttribute("aria-expanded")).toBe("false");
  expect(value.sessionRequests.filter((request) => request.projectId === first.id)).toHaveLength(0);

  fireEvent.click(firstGroup);
  expect(await screen.findByRole("button", { name: /Worktree First session/ })).toBeTruthy();
  fireEvent.click(secondGroup);
  expect(await screen.findByRole("button", { name: /Local computer Second session/ })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: `Empty project. Project ID: ${empty.id}` }));
  await waitFor(() => expect(screen.getAllByText("No conversations loaded.")).toHaveLength(1));

  expect(value.sessionRequests.filter((request) => request.projectId === first.id)).toHaveLength(1);
  expect(value.sessionRequests.filter((request) => request.projectId === second.id)).toHaveLength(1);
  expect(value.sessionRequests.filter((request) => request.projectId === empty.id)).toHaveLength(1);
  expect(value.openSession).not.toHaveBeenCalled();
});

it("includes the safe title reason in sidebar text and its accessible description", async () => {
  const project = resource(EntityKind.PROJECT, "Title project");
  const session = resource(EntityKind.SESSION, "New session", project.id, {
    workspace: "worktree", outcome: "succeeded", archive: "active", name_mode: "automatic", title_state: "skipped", title_reason: "budget-reached",
  });
  mountSidebar({ projects: () => ({ resources: [project] }), sessions: () => ({ sessions: [session] }) });
  fireEvent.click(await screen.findByRole("button", { name: `Title project. Project ID: ${project.id}` }));
  const row = await screen.findByRole("button", { name: /Title skipped\. The session budget did not allow another request\./ });
  expect(within(row).getByText("Skipped · Budget reached")).toBeTruthy();
});

it("keeps sparse global paging reachable and groups retained sessions by their original unknown project ID", async () => {
  const listedProject = resource(EntityKind.PROJECT, "Listed project");
  const missingParent = newRequestId();
  const owned = resource(EntityKind.SESSION, "Listed session", listedProject.id, { workspace: "worktree", outcome: "succeeded", archive: "active" });
  const general = resource(EntityKind.SESSION, "General session", "", { workspace: "general-chat", outcome: "not-started", archive: "active" });
  const retained = resource(EntityKind.SESSION, "Retained missing-parent session", missingParent, { workspace: "local", outcome: "stopped", archive: "archived" });
  const value = mountSidebar({
    projects: () => ({ resources: [listedProject] }),
    sessions: (request) => request.projectId ? { sessions: [] } : request.pageToken === "" ? { sessions: [owned], nextPageToken: "global-next" } : { sessions: [general, retained] },
  });

  await screen.findByRole("button", { name: `Listed project. Project ID: ${listedProject.id}` });
  await waitFor(() => expect(value.sessionRequests).toHaveLength(1));
  reach("sessions");

  expect(await screen.findByRole("button", { name: /General Chat General session/ })).toBeTruthy();
  const fallback = await screen.findByRole("button", { name: `Project · ${missingParent}. Project ID: ${missingParent}` });
  expect(fallback.getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByText("Project details are not in the loaded project catalog.")).toBeTruthy();
  const retainedRow = screen.getByRole("button", { name: /Local computer Retained missing-parent session/ });
  expect(within(fallback.parentElement!).getByRole("button", { name: /Local computer Retained missing-parent session/ })).toBe(retainedRow);
  expect(value.sessionRequests.some((request) => request.projectId === missingParent)).toBe(false);
  expect(value.sessionRequests.some((request) => request.projectId === "" && request.pageToken === "global-next")).toBe(true);
  expect(value.openSession).not.toHaveBeenCalled();
});

it("keeps catalog and session cursors independent and resets every session scope when archive visibility changes", async () => {
  const first = resource(EntityKind.PROJECT, "Project one");
  const second = resource(EntityKind.PROJECT, "Project two");
  const one = resource(EntityKind.SESSION, "One", first.id, { workspace: "worktree", outcome: "not-started", archive: "active" });
  const two = resource(EntityKind.SESSION, "Two", second.id, { workspace: "local", outcome: "running", archive: "active" });
  const value = mountSidebar({
    projects: (page) => page ? { resources: [second] } : { resources: [first], nextPageToken: "project-next" },
    sessions: (request) => {
      if (request.projectId === first.id) return request.pageToken ? { sessions: [one] } : { sessions: [one], nextPageToken: "one-next" };
      if (request.projectId === second.id) return { sessions: [two] };
      return request.pageToken ? { sessions: [], nextPageToken: "global-last" } : { sessions: [], nextPageToken: "global-next" };
    },
  });

  const oneGroup = await screen.findByRole("button", { name: `Project one. Project ID: ${first.id}` });
  fireEvent.click(oneGroup);
  reach("Project one sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === first.id && request.pageToken === "one-next")).toBe(true));
  reach("sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === "" && request.pageToken === "global-next")).toBe(true));
  reach("projects");
  expect(await screen.findByRole("button", { name: `Project two. Project ID: ${second.id}` })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: `Project two. Project ID: ${second.id}` }));
  await screen.findByRole("button", { name: /Local computer Two/ });

  const catalogReadsBeforeArchive = value.projectRequests.length;
  fireEvent.click(screen.getByRole("button", { name: "Project and conversation options" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Include archived" }));
  await waitFor(() => expect(value.sessionRequests.some((request) => request.includeArchived && request.projectId === "" && request.pageToken === "")).toBe(true));
  expect(value.sessionRequests.some((request) => request.includeArchived && request.projectId === second.id && request.pageToken === "")).toBe(true);
  expect(value.projectRequests).toHaveLength(catalogReadsBeforeArchive);
  expect(screen.getByRole("button", { name: `Project one. Project ID: ${first.id}` })).toBeTruthy();
  await waitFor(() => expect(value.sessionRequests.some((request) => request.includeArchived && request.projectId === first.id && request.pageToken === "")).toBe(true));
});

it("shows unknown execution and archive states without inventing successful or active indicators", async () => {
  const unknown = resource(EntityKind.SESSION, "Unknown state", "", { workspace: "mystery-workspace", outcome: "waiting-for-oracle", archive: "suspended" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [unknown] }) });
  const row = await screen.findByRole("button", { name: /Unknown workspace \(mystery-workspace\) Unknown state\. Execution state: waiting-for-oracle\. Archive state: suspended\./ });
  expect(row.querySelectorAll(".sidebar-status-unknown")).toHaveLength(2);
  expect(row.querySelector(".sidebar-status-running")).toBeNull();
  expect(row.querySelector("button")).toBeNull();
  expect(row.getAttribute("aria-describedby")).toBeTruthy();
  fireEvent.focus(row);
  const card = await screen.findByRole("tooltip");
  expect(within(card).getByText("Unknown state")).toBeTruthy();
  expect(within(card).getByText("Unknown workspace (mystery-workspace)")).toBeTruthy();
  expect(within(card).getByText("waiting-for-oracle")).toBeTruthy();
  expect(within(card).getByText("suspended")).toBeTruthy();
  expect(card.querySelector(".sidebar-session-card-title-state")).toBeNull();
  expect(window.document.getElementById(row.getAttribute("aria-describedby")!)?.textContent).toContain("Execution state: waiting-for-oracle. Archive state: suspended.");
  expect(value.openSession).not.toHaveBeenCalled();
});

function advanceHover(ms: number) { act(() => { vi.advanceTimersByTime(ms); }); }

it("delays pointer entry, bridges the card gap and keeps card scrolling independent", async () => {
  const session = resource(EntityKind.SESSION, "Hover session", "", { workspace: "worktree", outcome: "not-started", archive: "active" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Worktree Hover session/ });
  const reads = value.sessionRequests.length;
  vi.useFakeTimers();
  fireEvent.pointerEnter(row);
  advanceHover(299);
  expect(screen.queryByRole("tooltip")).toBeNull();
  advanceHover(1);
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Not started")).toBeTruthy();
  expect(within(card).getByText("Active")).toBeTruthy();
  fireEvent.pointerLeave(row);
  advanceHover(149);
  fireEvent.pointerEnter(card);
  advanceHover(1000);
  fireEvent.scroll(card);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.pointerLeave(card);
  advanceHover(149);
  expect(screen.getByRole("tooltip")).toBe(card);
  advanceHover(1);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(value.sessionRequests).toHaveLength(reads);
  expect(value.openSession).not.toHaveBeenCalled();
});

it("opens immediately on focus, retains focus across pointer departure and preserves activation", async () => {
  const session = resource(EntityKind.SESSION, "Focused session", "", { workspace: "local", outcome: "failed", archive: "archived" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Local computer Focused session/ });
  vi.useFakeTimers();
  act(() => row.focus());
  const card = screen.getByRole("tooltip");
  expect(document.activeElement).toBe(row);
  fireEvent.pointerEnter(row);
  fireEvent.pointerLeave(row);
  advanceHover(300);
  expect(screen.getByRole("tooltip")).toBe(card);
  fireEvent.click(row);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(value.openSession).toHaveBeenCalledExactlyOnceWith(session.id);
  expect(document.activeElement).toBe(row);
});

it("cancels pending hover on Escape and waits for a new entry after dismissal", async () => {
  const session = resource(EntityKind.SESSION, "Escape session", "", { workspace: "general-chat", outcome: "running", archive: "active" });
  mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /General Chat Escape session/ });
  vi.useFakeTimers();
  fireEvent.pointerEnter(row);
  advanceHover(100);
  fireEvent.keyDown(document, { key: "Escape" });
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerMove(row);
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerLeave(row);
  fireEvent.pointerEnter(row);
  advanceHover(300);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(row, { key: "Escape" });
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
  act(() => row.focus());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(row, { key: "Escape" });
  expect(document.activeElement).toBe(row);
  advanceHover(500);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

it("has one card and clears active or pending presentation on scrolling and surface changes", async () => {
  const first = resource(EntityKind.SESSION, "First hover", "", { workspace: "worktree", outcome: "succeeded", archive: "active" });
  const second = resource(EntityKind.SESSION, "Second hover", "", { workspace: "general-chat", outcome: "stopped", archive: "archiving" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [first, second] }) });
  const one = await screen.findByRole("button", { name: /Worktree First hover/ });
  const two = screen.getByRole("button", { name: /General Chat Second hover/ });
  vi.useFakeTimers();
  act(() => one.focus());
  act(() => two.focus());
  expect(screen.getAllByRole("tooltip")).toHaveLength(1);
  expect(within(screen.getByRole("tooltip")).getByText("Second hover")).toBeTruthy();
  fireEvent.scroll(screen.getByLabelText("Project and session navigation"));
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerEnter(one);
  value.setSurface(Surface.NewSession);
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerLeave(one);
  fireEvent.pointerEnter(one);
  advanceHover(300);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  value.setSurface(Surface.Usage);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

it("clears pending presentation when navigation is inactive or its source disappears", async () => {
  const session = resource(EntityKind.SESSION, "Disposed hover", "", { workspace: "local", outcome: "running", archive: "active" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Local computer Disposed hover/ });
  vi.useFakeTimers();
  fireEvent.pointerEnter(row);
  value.setProps({ homeActive: false });
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  value.setProps({ homeActive: true });
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.pointerLeave(row);
  fireEvent.pointerEnter(row);
  fireEvent.click(screen.getByRole("button", { name: "General Chat" }));
  advanceHover(300);
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(row.isConnected).toBe(false);
});

it("places measured cards below or above when the right side cannot fit and clears on resize", async () => {
  const session = resource(EntityKind.SESSION, "Edge hover", "", { workspace: "local", outcome: "running", archive: "active" });
  mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Local computer Edge hover/ });
  vi.stubGlobal("innerWidth", 960);
  vi.stubGlobal("innerHeight", 640);
  const rect = (left: number, top: number, width: number, height: number) => ({ x: left, y: top, left, top, width, height, right: left + width, bottom: top + height, toJSON: () => ({}) });
  let sourceTop = 100;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return this === row ? rect(700, sourceTop, 100, 34) : this.classList.contains("sidebar-session-tooltip") ? rect(0, 0, 320, 220) : rect(0, 0, 0, 0);
  });
  fireEvent.focus(row);
  let card = screen.getByRole("tooltip");
  expect(card.style.left).toBe("632px");
  expect(card.style.top).toBe("142px");
  fireEvent(window, new Event("resize"));
  expect(screen.queryByRole("tooltip")).toBeNull();
  sourceTop = 500;
  fireEvent.blur(row);
  fireEvent.focus(row);
  card = screen.getByRole("tooltip");
  expect(card.style.left).toBe("632px");
  expect(card.style.top).toBe("272px");
});

it.each([
  ["not-started", "active", "Not started", "Active"],
  ["running", "archiving", "Running", "Archiving"],
  ["succeeded", "archived", "Succeeded", "Archived"],
  ["failed", "active", "Failed", "Active"],
  ["stopped", "archived", "Stopped", "Archived"],
])("keeps execution %s and archive %s as separate labeled observations", async (outcome, archive, executionText, archiveText) => {
  const session = resource(EntityKind.SESSION, "State hover", "", { workspace: "worktree", outcome, archive });
  mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  fireEvent.focus(await screen.findByRole("button", { name: /Worktree State hover/ }));
  const card = screen.getByRole("tooltip");
  expect(within(card).getByText("Execution").nextElementSibling?.textContent).toBe(executionText);
  expect(within(card).getByText("Archive").nextElementSibling?.textContent).toBe(archiveText);
  expect(card.querySelector("button, a, input, [tabindex]")).toBeNull();
});

it.each([
  ["waiting", "", "Title waits for the first completed turn", ""],
  ["queued", "", "Title queued", ""],
  ["running", "", "Generating title", ""],
  ["succeeded", "", "Title generated", ""],
  ["skipped", "budget-reached", "Title skipped", "The session budget did not allow another request."],
  ["failed", "inference-failed", "Title generation failed", "The title request failed; the conversation remains available."],
  ["unsupported", "unsupported-agent-profile", "Title generation unsupported", "The original Agent profile does not support title generation."],
  ["unsupported", "worker-capability-absent", "Title generation unsupported", "The original Worker did not prove the required title capability."],
  ["skipped", "canceled", "Title skipped", "Title generation was canceled with the session operation."],
  ["failed", "authority-lost", "Title generation failed", "The original account or session permission is no longer available."],
  ["failed", "invalid-output", "Title generation failed", "The title result did not pass validation."],
  ["uncertain", "cleanup-uncertain", "Title outcome uncertain", "Worker cleanup is uncertain; inspect the retained operation."],
  ["skipped", "manual-rename", "Title skipped", "A manual rename now owns this title."],
  ["future-title", "", "Unknown title state (future-title)", ""],
  ["", "", "Title state unavailable", ""],
])("retains automatic title state %s and its safe reason", async (state, reason, label, detail) => {
  const session = resource(EntityKind.SESSION, "Title hover", "", { workspace: "worktree", outcome: "succeeded", archive: "active", name_mode: "automatic", title_state: state, title_reason: reason });
  mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Worktree Title hover/ });
  fireEvent.focus(row);
  const card = screen.getByRole("tooltip");
  expect(card.querySelector(".sidebar-session-card-title-state")?.textContent).toContain(label);
  if (detail) expect(card.querySelector(".sidebar-session-card-title-state")?.textContent).toContain(detail);
  expect(document.getElementById(row.getAttribute("aria-describedby")!)?.textContent).toContain(label);
});

it("omits automatic title information and its divider after a manual rename", async () => {
  const session = resource(EntityKind.SESSION, "My chosen title", "", { workspace: "worktree", outcome: "succeeded", archive: "active", name_mode: "manual", title_state: "skipped", title_reason: "manual-rename" });
  mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Worktree My chosen title/ });
  fireEvent.focus(row);
  const card = screen.getByRole("tooltip");
  expect(card.querySelector(".sidebar-session-card-title-state")).toBeNull();
  expect(card.textContent).not.toContain("Title skipped");
  expect(row.querySelector(".sidebar-session-title-state")).toBeNull();
  expect(document.getElementById(row.getAttribute("aria-describedby")!)?.textContent).not.toContain("Title skipped");
});

it("updates an open card in Korean without losing focus or adding reads and preserves the full name", async () => {
  const name = "A complete long session name ".repeat(8);
  const session = resource(EntityKind.SESSION, name, "", { workspace: "local", outcome: "failed", archive: "archived", name_mode: "automatic", title_state: "failed", title_reason: "future-reason" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [session] }) });
  const row = await screen.findByRole("button", { name: /Local computer A complete long session name/ });
  act(() => row.focus());
  const card = screen.getByRole("tooltip");
  expect(card.querySelector(".sidebar-session-card-title")?.textContent).toBe(name);
  expect(card.textContent).toContain("future-reason");
  const reads = value.sessionRequests.length;
  await act(async () => { await i18n.changeLanguage(SupportedLanguage.Korean); });
  expect(screen.getByRole("tooltip")).toBe(card);
  expect(within(card).getByText("실행")).toBeTruthy();
  expect(within(card).getByText("실패")).toBeTruthy();
  expect(within(card).getByText("보관됨")).toBeTruthy();
  expect(card.querySelector(".sidebar-session-card-title")?.textContent).toBe(name);
  expect(document.activeElement).toBe(row);
  expect(value.sessionRequests).toHaveLength(reads);
});

it("distinguishes permission and connection failures from successful empty pages", async () => {
  mountSidebar({
    projects: () => { throw new ConnectError("Denied", Code.PermissionDenied); },
    sessions: () => { throw new ConnectError("Offline", Code.Unavailable); },
  });
  expect(await screen.findByText("You do not have permission to view projects.")).toBeTruthy();
  expect(await screen.findByText("Could not connect to load sessions.")).toBeTruthy();
  expect(screen.queryByText("No projects loaded.")).toBeNull();
  expect(screen.queryByText("No conversations loaded.")).toBeNull();
});

it("keeps cached rows visible and labels them as previous data after a failed refresh", async () => {
  const project = resource(EntityKind.PROJECT, "Cached project");
  let projectReads = 0;
  const value = mountSidebar({
    projects: () => { projectReads++; if (projectReads > 1) throw new ConnectError("Offline", Code.Unavailable); return { resources: [project] }; },
    sessions: () => ({ sessions: [] }),
  });
  const row = await screen.findByRole("button", { name: `Cached project. Project ID: ${project.id}` });
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  expect(await screen.findByText("Could not refresh projects. Previous data is shown.")).toBeTruthy();
  expect(screen.getByRole("button", { name: `Cached project. Project ID: ${project.id}` })).toBe(row);
  expect(screen.queryByRole("button", { name: "Refresh projects and sessions" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Retry project catalog" }));
  await waitFor(() => expect(value.projectRequests).toHaveLength(3));
  expect(value.projectRequests).toEqual(["", "", ""]);
  expect(screen.getByRole("button", { name: `Cached project. Project ID: ${project.id}` })).toBe(row);
});

it("retries only the failed catalog and global-session pages", async () => {
  const first = resource(EntityKind.PROJECT, "First project");
  const second = resource(EntityKind.PROJECT, "Second project");
  const global = resource(EntityKind.SESSION, "First global session");
  let catalogNextReads = 0, globalNextReads = 0;
  const value = mountSidebar({
    projects: (page) => {
      if (!page) return { resources: [first], nextPageToken: "project-next" };
      if (++catalogNextReads === 1) throw new ConnectError("Catalog unavailable", Code.Unavailable);
      return { resources: [second] };
    },
    sessions: (request) => {
      if (request.projectId) return { sessions: [] };
      if (!request.pageToken) return { sessions: [global], nextPageToken: "global-next" };
      if (++globalNextReads === 1) throw new ConnectError("Sessions unavailable", Code.Unavailable);
      return { sessions: [] };
    },
  });

  await screen.findByRole("button", { name: `First project. Project ID: ${first.id}` });
  reach("projects");
  expect(await screen.findByRole("button", { name: "Retry project catalog" })).toBeTruthy();
  reach("sessions");
  expect(await screen.findByRole("button", { name: "Retry global sessions" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry project catalog" }));
  expect(await screen.findByRole("button", { name: `Second project. Project ID: ${second.id}` })).toBeTruthy();
  expect(value.projectRequests).toEqual(["", "project-next", "project-next"]);
  expect(value.sessionRequests.map(({ projectId, pageToken }) => [projectId, pageToken])).toEqual([["", ""], ["", "global-next"]]);
  expect(screen.getByRole("button", { name: "Retry global sessions" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry global sessions" }));
  await waitFor(() => expect(value.sessionRequests).toHaveLength(3));
  expect(value.sessionRequests[2]).toMatchObject({ projectId: "", pageToken: "global-next", includeArchived: false });
  expect(value.projectRequests).toEqual(["", "project-next", "project-next"]);
});

it("retries an expanded project's exact session page without refetching other scopes", async () => {
  const project = resource(EntityKind.PROJECT, "Named project");
  const row = resource(EntityKind.SESSION, "Project session", project.id);
  let nextReads = 0;
  const value = mountSidebar({
    projects: () => ({ resources: [project] }),
    sessions: (request) => {
      if (!request.projectId) return { sessions: [] };
      if (!request.pageToken) return { sessions: [row], nextPageToken: "project-session-next" };
      if (++nextReads === 1) throw new ConnectError("Project sessions unavailable", Code.Unavailable);
      return { sessions: [row] };
    },
  });
  fireEvent.click(await screen.findByRole("button", { name: `Named project. Project ID: ${project.id}` }));
  reach("Named project sessions");
  expect(await screen.findByRole("button", { name: "Retry Named project sessions" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Retry Named project sessions" }));
  await waitFor(() => expect(value.sessionRequests.filter((request) => request.projectId === project.id)).toHaveLength(3));
  expect(value.sessionRequests.filter((request) => request.projectId === project.id).map((request) => request.pageToken)).toEqual(["", "project-session-next", "project-session-next"]);
  expect(value.projectRequests).toEqual([""]);
  expect(value.sessionRequests.filter((request) => !request.projectId)).toHaveLength(1);
});

it("routes the icon rail to the matching surface and opens New project through Settings", async () => {
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [] }) });
  await screen.findByRole("button", { name: "Sessions" });
  const newProject = screen.getByRole("button", { name: "New project" });
  expect(newProject.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  expect(newProject.textContent).toBe("");
  fireEvent.focus(newProject);
  expect(window.document.querySelector(".sidebar-action-tooltip")?.textContent).toBe("New project");
  fireEvent.click(newProject);
  expect(value.openSettings).toHaveBeenCalledWith("new-project");
  fireEvent.click(screen.getByRole("button", { name: "Pull requests" }));
  expect(value.navigate).toHaveBeenCalledWith(Surface.PullRequests);
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expect(value.newSession).toHaveBeenCalledOnce();
  expect(value.sessionRequests).toHaveLength(1);
});

function reach(label: string, remaining = 96) {
  const root = window.document.querySelector<HTMLDivElement>(".sidebar-list")!;
  Object.defineProperty(root, "clientHeight", { configurable: true, value: 400 });
  const rect = (top: number) => ({ top, bottom: top + 1, left: 0, right: 200, width: 200, height: 1, x: 0, y: top, toJSON: () => ({}) });
  vi.spyOn(root, "getBoundingClientRect").mockReturnValue({ ...rect(0), bottom: 400, height: 400 });
  for (const anchor of root.querySelectorAll<HTMLElement>("[data-continuation]")) {
    vi.spyOn(anchor, "getBoundingClientRect").mockReturnValue(rect(600));
  }
  const anchor = root.querySelector<HTMLElement>(`[data-continuation="${label}"]`)!;
  vi.mocked(anchor.getBoundingClientRect).mockReturnValueOnce(rect(400 + remaining));
  fireEvent.scroll(root);
}

it("requires the visible anchor to be within 96px and retains focus and scroll on append", async () => {
  const first = resource(EntityKind.SESSION, "First visible", "", { workspace: "general-chat" });
  const second = resource(EntityKind.SESSION, "Additional visible", "", { workspace: "general-chat" });
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: ({ pageToken }) => ({ sessions: pageToken ? [first, second] : [first], nextPageToken: pageToken ? "" : "next" }) });
  const row = await screen.findByRole("button", { name: /First visible/ });
  row.focus();
  const list = window.document.querySelector<HTMLDivElement>(".sidebar-list")!;
  list.scrollTop = 42;
  reach("sessions", 97);
  expect(value.sessionRequests).toHaveLength(1);
  reach("sessions", 96);
  await screen.findByRole("button", { name: /Additional visible/ });
  expect(value.sessionRequests.map(({ pageToken }) => pageToken)).toEqual(["", "next"]);
  expect(window.document.activeElement).toBe(row);
  expect(list.scrollTop).toBe(42);
  expect(screen.getAllByRole("button", { name: /First visible/ })).toHaveLength(1);
  expect(screen.queryByRole("button", { name: /First page|Next page|Load more|Next session|Next project/ })).toBeNull();
});

it("discards delayed named continuations on collapse and preserves accepted rows across Home navigation", async () => {
  const project = resource(EntityKind.PROJECT, "Delayed");
  const first = resource(EntityKind.SESSION, "Accepted", project.id);
  const late = resource(EntityKind.SESSION, "Late", project.id);
  let release!: (result: { sessions: Resource[]; nextPageToken: string }) => void;
  const value = mountSidebar({ projects: () => ({ resources: [project] }), sessions: ({ projectId, pageToken }) => !projectId ? { sessions: [] } : !pageToken ? { sessions: [first], nextPageToken: "next" } : new Promise((resolve) => { release = resolve; }) });
  const group = await screen.findByRole("button", { name: `Delayed. Project ID: ${project.id}` });
  fireEvent.click(group);
  await screen.findByRole("button", { name: /Accepted/ });
  reach("Delayed sessions");
  await waitFor(() => expect(value.sessionRequests.some(({ pageToken }) => pageToken === "next")).toBe(true));
  reach("Delayed sessions");
  expect(value.sessionRequests.filter(({ pageToken }) => pageToken === "next")).toHaveLength(1);
  fireEvent.click(group);
  await act(async () => release({ sessions: [late], nextPageToken: "unseen" }));
  fireEvent.click(group);
  await screen.findByRole("button", { name: /Accepted/ });
  expect(screen.queryByRole("button", { name: /Late/ })).toBeNull();
  value.setProps({ surface: Surface.NewSession });
  expect(screen.getByRole("button", { name: /Accepted/ })).toBeTruthy();
  value.setProps({ surface: Surface.Activity });
  expect(screen.queryByRole("button", { name: /Accepted/ })).toBeNull();
  value.setProps({ surface: Surface.Sessions });
  expect(screen.getByRole("button", { name: /Accepted/ })).toBeTruthy();
});

it("transfers expanded fallback rows until the authoritative named read accepts success", async () => {
  const project = resource(EntityKind.PROJECT, "Resolved parent");
  const retained = resource(EntityKind.SESSION, "Retained fallback", project.id, { workspace: "local" });
  let accept!: (result: { sessions: Resource[] }) => void;
  const value = mountSidebar({ projects: (token) => token ? { resources: [project] } : { resources: [], nextPageToken: "catalog-next" }, sessions: ({ projectId }) => projectId ? new Promise((resolve) => { accept = resolve; }) : { sessions: [retained] } });
  fireEvent.click(await screen.findByRole("button", { name: /Retained fallback/ }));
  value.setProps({ selectedSessionId: retained.id });
  const originalRow = screen.getByRole("button", { name: /Retained fallback/ });
  originalRow.focus();
  reach("projects");
  const named = await screen.findByRole("button", { name: `Resolved parent. Project ID: ${project.id}` });
  expect(named.getAttribute("aria-expanded")).toBe("true");
  const row = screen.getByRole("button", { name: /Retained fallback/ });
  expect(row).toBe(originalRow);
  expect(window.document.activeElement).toBe(originalRow);
  expect(row.getAttribute("aria-current")).toBe("true");
  expect(within(named.parentElement!).getByRole("button", { name: /Retained fallback/ })).toBe(row);
  await waitFor(() => expect(accept).toBeTypeOf("function"));
  await act(async () => accept({ sessions: [retained] }));
  expect(screen.getByRole("button", { name: /Retained fallback/ }).getAttribute("aria-current")).toBe("true");
  expect(value.openSession).toHaveBeenCalledOnce();
});

it("keeps the archive popup open for changes and restores its opener on Escape", async () => {
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [] }) });
  await screen.findByText("No projects loaded.");
  fireEvent.click(screen.getByRole("button", { name: "Create a project" }));
  expect(value.openSettings).toHaveBeenCalledWith("new-project");
  const opener = screen.getByRole("button", { name: "Project and conversation options" });
  fireEvent.click(opener);
  const popup = screen.getByRole("dialog", { name: "Project and conversation options" });
  fireEvent.click(within(popup).getByRole("checkbox", { name: "Include archived" }));
  await screen.findByText("Archived included");
  expect(screen.getByRole("dialog", { name: "Project and conversation options" })).toBe(popup);
  fireEvent.keyDown(popup, { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: "Project and conversation options" })).toBeNull();
  expect(window.document.activeElement).toBe(opener);
});

it("shows generic connection status with the saved name and no lifecycle disclosure", async () => {
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [] }), props: { serverPresentation: { kind: ServerPresentationKind.Saved, name: "Original saved name" } } });
  const summary = await screen.findByText("Original saved name · Connected");
  expect(summary.getAttribute("role")).toBe("status");
  expect(window.document.querySelector("#sidebar-server-management")).toBeNull();
  expect(screen.queryByText("Server 0.1.0")).toBeNull();
  expect(screen.queryByRole("button", { name: /Original saved name/ })).toBeNull();
  value.setProps({ serverPresentation: { kind: ServerPresentationKind.Local } });
  expect(screen.getByText("This computer · Connected")).toBe(summary);
  value.setProps({ connectionReady: false });
  expect(screen.getByText("This computer · Disconnected · previous data may be stale")).toBe(summary);
});


function expectHeaderActions(home: boolean) {
  const header = window.document.querySelector<HTMLElement>(".sidebar-header")!;
  expect(within(header).getByRole("heading", { name: "DeliDev" })).toBeTruthy();
  expect([...header.querySelectorAll("button")].map((button) => button.getAttribute("aria-label"))).toEqual(home ? ["Inbox", "Search"] : []);
  expect(header.querySelector(".sidebar-header-actions") === null).toBe(!home);
  for (const label of ["Inbox", "Search"]) {
    const action = within(header).queryByRole("button", { name: label });
    if (home) expect(action?.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    else expect(action).toBeNull();
  }
}

it.each(["loading", "empty", "denied", "unavailable", "cached-refresh"])("shows home-only header actions independently of %s reads", async (mode) => {
  let finish!: () => void;
  const gate = new Promise<void>((resolve) => { finish = resolve; });
  let failedRefresh = false;
  const value = mountSidebar({ stateful: true,
    projects: async () => {
      if (mode === "loading") await gate;
      if (mode === "denied" || mode === "unavailable" || failedRefresh) throw new ConnectError("Fixture read failure", mode === "denied" ? Code.PermissionDenied : Code.Unavailable);
      return { resources: [] };
    }, sessions: async () => { if (mode === "loading") await gate; return { sessions: [] }; },
  });
  if (mode === "loading") await screen.findByText("Loading projects…");
  else if (mode === "denied") await screen.findByText("You do not have permission to view projects.");
  else if (mode === "unavailable") await screen.findByText("Could not connect to load projects.");
  else await screen.findByText("No projects loaded.");
  if (mode === "cached-refresh") {
    failedRefresh = true;
    await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
    await screen.findByText("Could not refresh projects. Previous data is shown.");
  }
  expectHeaderActions(true);
  fireEvent.click(screen.getByRole("button", { name: "New session" }));
  expectHeaderActions(true);
  const reads = [value.projectRequests.length, value.sessionRequests.length];
  for (const surface of [Surface.PullRequests, Surface.Usage, Surface.Schedules, Surface.Activity, Surface.Inbox, Surface.Search]) {
    value.setSurface(surface);
    expectHeaderActions(false);
    expect([value.projectRequests.length, value.sessionRequests.length]).toEqual(reads);
  }
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expectHeaderActions(true);
  expect(value.openSession).not.toHaveBeenCalled();
  expect(value.openSettings).not.toHaveBeenCalled();
  finish();
});

it("retains sidebar page scopes, group expansion and per-surface scroll through header navigation", async () => {
  const project = resource(EntityKind.PROJECT, "Retained project");
  const value = mountSidebar({ stateful: true,
    projects: (page) => ({ resources: [project], nextPageToken: page ? undefined : "project-next" }),
    sessions: (request) => ({ sessions: [], nextPageToken: request.pageToken ? undefined : request.projectId ? "group-next" : "global-next" }),
  });
  fireEvent.click(await screen.findByRole("button", { name: `Retained project. Project ID: ${project.id}` }));
  await waitFor(() => expect(screen.queryByText("Loading Retained project sessions…")).toBeNull());
  reach("Retained project sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.projectId === project.id && request.pageToken === "group-next")).toBe(true));
  reach("projects");
  await waitFor(() => expect(value.projectRequests).toContain("project-next"));
  reach("sessions");
  await waitFor(() => expect(value.sessionRequests.some((request) => request.pageToken === "global-next")).toBe(true));
  const list = window.document.querySelector<HTMLElement>(".sidebar-list")!;
  list.scrollTop = 180;
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  expectHeaderActions(false);
  expect(list.scrollTop).toBe(0);
  list.scrollTop = 45;
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expectHeaderActions(true);
  expect(list.scrollTop).toBe(180);
  expect(screen.getByRole("button", { name: `Retained project. Project ID: ${project.id}` }).getAttribute("aria-expanded")).toBe("true");
  expect(screen.queryByRole("button", { name: /First project page|First session page|First page of Retained project sessions|Next page/ })).toBeNull();
  await waitFor(() => {
    expect(value.projectRequests).toContain("project-next");
    expect(value.sessionRequests.some((request) => !request.projectId && request.pageToken === "global-next")).toBe(true);
    expect(value.sessionRequests.some((request) => request.projectId === project.id && request.pageToken === "group-next")).toBe(true);
  });
  fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
  expect(list.scrollTop).toBe(45);
  expect(value.openSession).not.toHaveBeenCalled();
});


it.each([false, true])("keeps global recovery visible with General Chat collapsed (expired=%s)", async (expired) => {
  const parent = newRequestId();
  const retained = resource(EntityKind.SESSION, "Visible fallback after failure", parent, { workspace: "local" });
  let failed = false;
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: ({ projectId }) => {
    expect(projectId).toBe("");
    if (failed) throw new ConnectError("Fixture read failure", expired ? Code.OutOfRange : Code.Unavailable, undefined, expired ? [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: FailureCode.CursorExpired }) }] : []);
    return { sessions: [retained] };
  } });
  const row = await screen.findByRole("button", { name: /Visible fallback after failure/ });
  value.setProps({ selectedSessionId: retained.id });
  row.focus();
  const general = screen.getByRole("button", { name: "General Chat" });
  fireEvent.click(general);
  expect(general.getAttribute("aria-expanded")).toBe("false");
  failed = true;
  await act(async () => { await value.client.invalidateQueries({ refetchType: "active" }); });
  expect(await screen.findByText(expired ? "sessions: The list cursor expired." : "Could not refresh sessions. Previous data is shown.")).toBeTruthy();
  const recovery = screen.getByRole("button", { name: expired ? "Reload sessions list" : "Retry global sessions" });
  expect(recovery.closest(".sidebar-general-chat")).toBeNull();
  expect(screen.getByRole("button", { name: /Visible fallback after failure/ })).toBe(row);
  expect(row.getAttribute("aria-current")).toBe("true");
  expect(document.activeElement).toBe(row);
  const catalogReads = [...value.projectRequests];
  const globalReads = value.sessionRequests.length;
  failed = false;
  fireEvent.click(recovery);
  await waitFor(() => expect(screen.queryByRole("button", { name: /Retry global sessions|Reload sessions list/ })).toBeNull());
  expect(value.sessionRequests).toHaveLength(globalReads + 1);
  expect(value.sessionRequests.at(-1)?.pageToken).toBe("");
  expect(value.projectRequests).toEqual(catalogReads);
  expect(general.getAttribute("aria-expanded")).toBe("false");
  expect(screen.getByRole("button", { name: /Visible fallback after failure/ })).toBe(row);
  expect(value.openSession).not.toHaveBeenCalled();
});
