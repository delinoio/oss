import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { Surface } from "./views";
import { Sidebar } from "./sidebar";
import type { ComponentProps } from "react";
import { ServerPresentationKind } from "./server-presentation";

function resource(kind: EntityKind, name: string, projectId = "", values: Record<string, unknown> = {}): Resource {
  const id = newRequestId();
  const row = create(ResourceSchema, { id, sessionId: kind === EntityKind.SESSION ? id : "", projectId, kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name, ...values }) });
  return row;
}

function mountSidebar({ projects, sessions, props = {} }: {
  projects: (pageToken: string) => { resources: Resource[]; nextPageToken?: string } | Promise<{ resources: Resource[]; nextPageToken?: string }>;
  sessions: (request: { projectId: string; includeArchived: boolean; pageToken: string }) => { sessions: Resource[]; nextPageToken?: string } | Promise<{ sessions: Resource[]; nextPageToken?: string }>;
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
  const newSession = vi.fn();
  const navigate = vi.fn();
  let currentProps = props;
  const tree = () => <TransportProvider transport={transport}><QueryClientProvider client={client}><Sidebar surface={Surface.Sessions} selectedSessionId="" navigate={navigate} openSession={openSession} newSession={newSession} openSettings={openSettings} {...currentProps} /></QueryClientProvider></TransportProvider>;
  const view = render(tree());
  const setProps = (next: Partial<ComponentProps<typeof Sidebar>>) => { currentProps = { ...currentProps, ...next }; view.rerender(tree()); };
  return { ...view, setProps, client, navigate, openSession, openSettings, newSession, projectRequests, sessionRequests };
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
  expect((await screen.findByRole("tooltip")).textContent).toContain("Execution state: waiting-for-oracle. Archive state: suspended. Workspace: Unknown workspace (mystery-workspace).");
  expect(value.openSession).not.toHaveBeenCalled();
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

it("keeps the same local/saved management controller mounted through disclosure without invoking actions", async () => {
  const operation = vi.fn();
  const value = mountSidebar({ projects: () => ({ resources: [] }), sessions: () => ({ sessions: [] }), props: { localServer: <button onClick={operation}>Existing operation</button>, serverPresentation: { kind: ServerPresentationKind.Saved, name: "Original saved name" } } });
  const summary = await screen.findByRole("button", { name: /Original saved name Server 0.1.0/ });
  const controller = window.document.querySelector("#sidebar-server-management")!;
  const button = within(controller as HTMLElement).getByRole("button", { name: "Existing operation", hidden: true });
  expect(screen.queryByRole("button", { name: "Existing operation" })).toBeNull();
  fireEvent.click(summary);
  expect(screen.getByRole("button", { name: "Existing operation" })).toBe(button);
  fireEvent.click(summary); fireEvent.click(summary);
  expect(window.document.querySelector("#sidebar-server-management")).toBe(controller);
  expect(operation).not.toHaveBeenCalled();
  expect(summary.querySelector(".sidebar-status-running")).toBeNull();
  value.setProps({ serverPresentation: { kind: ServerPresentationKind.Local } });
  expect(screen.getByRole("button", { name: /Local server Server 0.1.0/ })).toBe(summary);
});
