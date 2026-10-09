import { ProjectSettingsMenu } from "./project-settings-menu";
import { DisclosureButton, DisclosureContent, DisclosureDensity } from "./disclosure";
import { SubscriptionRail } from "./subscription-rail";
import { SessionRowActions, SessionRowActionsProvider, useSessionActionMenuOpen } from "./session-row-actions";
import { LocalizedText, copy, useLocale } from "./localization";
import { statusLabel } from "./product-status";
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type RefObject, type MouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";

import { useQuery } from "@connectrpc/connect-query";
import { FailureCode, SystemQuery } from "@delinoio/delidev-api-client";
import { Workspace, workspaceNames } from "./documents";
import { Surface } from "./views";
import { useShortcutHelp, useGlobalShortcutAria } from "./shortcut-provider";
import { ShortcutId } from "./shortcuts";
import type { SettingsNavigationEntry } from "./settings";
import { ScrollContinuation } from "./scroll-continuation";
import { HomeNavigation, ReadStage, type NavigationRow } from "./home-navigation";
import { HomeScope, useNavigationQuery } from "./home-navigation-query";
import { ServerPresentationKind, type ServerPresentation } from "./server-presentation";
import { SessionHoverCard, SessionHoverProvider, useSessionHover } from "./session-hover-card";

enum ExecutionStatus {
  NotStarted = "not-started",
  Running = "running",
  Succeeded = "succeeded",
  Failed = "failed",
  Stopped = "stopped",
}

enum ArchiveStatus {
  Active = "active",
  Archiving = "archiving",
  Archived = "archived",
}

type TooltipPosition = { left: number; top: number };

export function Icon({ name, className = "" }: { name: string; className?: string }) {
  useLocale();
  const common = { "aria-hidden": true as const, className: `sidebar-icon ${className}`, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  switch (name) {
    case "sessions": return <svg {...common}><path d="M4 10.5 12 4l8 6.5V20H4z"/><path d="M9 20v-6h6v6"/></svg>;
    case "pull-requests": return <svg {...common}><circle cx="6" cy="6" r="2"/><circle cx="18" cy="18" r="2"/><path d="M6 8v10a4 4 0 0 0 4 4M18 16V8a4 4 0 0 0-4-4h-2"/><path d="m12 2-2 2 2 2"/></svg>;
    case "usage": return <svg {...common}><path d="M4 20V12M10 20V5M16 20v-9M22 20V8"/></svg>;
    case "schedules": return <svg {...common}><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>;
    case "activity": return <svg {...common}><circle cx="5" cy="6" r="1"/><circle cx="5" cy="12" r="1"/><circle cx="5" cy="18" r="1"/><path d="M9 6h10M9 12h10M9 18h10"/></svg>;
    case "settings": return <svg {...common}><circle cx="12" cy="12" r="3"/><path d="m19.4 15 .1.1 1.2 2.1-2 2-2.1-1.2-.2.1-2.4 1v2.4h-2.8v-2.4l-2.4-1-.2-.1-2.1 1.2-2-2 1.2-2.1.1-.2-1-2.4H2.4v-2.8h2.4l1-2.4-.1-.2-1.2-2.1 2-2 2.1 1.2.2-.1 2.4-1V2.4h2.8v2.4l2.4 1 .2.1 2.1-1.2 2 2-1.2 2.1-.1.2 1 2.4h2.4v2.8h-2.4z" transform="translate(1 1) scale(.92)"/></svg>;
    case "inbox": return <svg {...common}><path d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4"/></svg>;
    case "search": return <svg {...common}><circle cx="10.8" cy="10.8" r="6.8"/><path d="m16 16 5 5"/></svg>;
    case "refresh": return <svg {...common}><path d="M20 7v5h-5M4 17v-5h5"/><path d="M5.6 9a7 7 0 0 1 11.7-2L20 12M4 12l2.7 5a7 7 0 0 0 11.7-2"/></svg>;
    case "folder": return <svg {...common}><path d="M3 6h7l2 2h9v11H3z"/></svg>;
    case "branch": return <svg {...common}><circle cx="6" cy="5" r="2"/><circle cx="18" cy="19" r="2"/><path d="M6 7v10a4 4 0 0 0 4 4h6M18 17V9a4 4 0 0 0-4-4h-2"/></svg>;
    case "computer": return <svg {...common}><rect x="3" y="4" width="18" height="13" rx="1.5"/><path d="M8 21h8M12 17v4"/></svg>;
    case "chat": return <svg {...common}><path d="M4 5h16v12H9l-5 4z"/><path d="M8 9h8M8 13h5"/></svg>;
    case "chat-plus": return <svg {...common}><path d="M4 5h16v12H9l-5 4z"/><path d="M12 8v6M9 11h6"/></svg>;
    case "help":
    case "unknown": return <svg {...common}><circle cx="12" cy="12" r="9"/><path d="M9.8 9a2.3 2.3 0 1 1 4.3 1.2c-.9 1.1-2.1 1.2-2.1 3M12 17h.01"/></svg>;
    case "options": return <svg {...common}><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>;
    case "server": return <svg {...common}><rect x="3" y="3" width="18" height="8" rx="2"/><rect x="3" y="13" width="18" height="8" rx="2"/><path d="M7 7h.01M7 17h.01M12 7h5M12 17h5"/></svg>;
    case "plus": return <svg {...common}><path d="M12 5v14M5 12h14"/></svg>;
    case "chevron": return <svg {...common}><path d="m8 10 4 4 4-4"/></svg>;
    default: return null;
  }
}

function SidebarButton({ label, icon, current, onClick, className = "" }: { label: string; icon: string; current?: boolean; onClick: (event: MouseEvent<HTMLButtonElement>) => void; className?: string }) {
  useLocale();
  return <button type="button" aria-label={label} aria-current={current ? "page" : undefined} className={`sidebar-rail-button ${className}`} onClick={onClick}>
    <Icon name={icon} /><span className="sidebar-rail-tooltip" aria-hidden="true">{label}</span>
  </button>;
}

type NavigationQuery = ReturnType<typeof useNavigationQuery>;
function QueryProblem({ query, label, retryLabel }: { query: NavigationQuery; label: string; retryLabel: string }) {
  useLocale();
  if (!query.error) return null;
  const { failure, stalled } = query.error;
  const reload = failure.code === FailureCode.CursorExpired || stalled;
  const message = reload ? `${label}: ${stalled ? copy("sidebar.extra.ad70acba0412") : copy("sidebar.extra.b36417b2a769")}` : query.loaded ? copy("sidebar.sentence.28902f557c4c", { v0: query.error.stage === ReadStage.Additional ? copy("sidebar.extra.de45c9bc43ff") : statusLabel("refresh"), v1: label }) : failure.code === FailureCode.PermissionDenied ? copy("sidebar.sentence.1c87008c72bf", { v0: label }) : copy("sidebar.sentence.3c9f0d81b95a", { v0: label });
  return <div className="sidebar-query-problem">
    <span role="status">{message}{failure.correlationId ? copy("sidebar.correlation_3851eb", { v0: failure.correlationId }) : ""}</span>
    <button type="button" aria-label={reload ? copy("sidebar.reloadList_83b8c9", { v0: label }) : retryLabel} disabled={Boolean(query.loading)} onClick={reload ? query.reload : query.retry}>{reload ? copy("sidebar.reloadList_095352") : copy("sidebar.retry_942087")}</button>
  </div>;
}


function workspaceLabel(raw: string): string {
  if (Object.values(Workspace).includes(raw as Workspace)) return workspaceNames[raw as Workspace];
  return raw ? copy("sidebar.sentence.7e9a504e5355", { v0: raw }) : copy("sidebar.extra.814a1748b7f0");
}

function executionLabel(raw: string): string {
  return statusLabel(raw || "unknown");
}

function archiveLabel(raw: string): string {
  return statusLabel(raw || "unknown");
}

function cardExecutionLabel(raw: string): string {
  switch (raw as ExecutionStatus) {
    case ExecutionStatus.NotStarted: return copy("sidebar.hover.notStarted");
    case ExecutionStatus.Running: return copy("sidebar.hover.running");
    case ExecutionStatus.Succeeded: return copy("sidebar.hover.succeeded");
    case ExecutionStatus.Failed: return copy("sidebar.hover.failed");
    case ExecutionStatus.Stopped: return copy("sidebar.hover.stopped");
    default: return executionLabel(raw);
  }
}

function cardArchiveLabel(raw: string): string {
  switch (raw as ArchiveStatus) {
    case ArchiveStatus.Active: return copy("sidebar.hover.active");
    case ArchiveStatus.Archiving: return copy("sidebar.hover.archiving");
    case ArchiveStatus.Archived: return copy("sidebar.hover.archived");
    default: return archiveLabel(raw);
  }
}

function StatusGlyph({ outcome, archive }: { outcome: string; archive: string }) {
  useLocale();
  let execution: ReactNode;
  switch (outcome as ExecutionStatus) {
    case ExecutionStatus.NotStarted: execution = <svg className="sidebar-status-glyph" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6"><circle cx="8" cy="8" r="5.25"/></svg>; break;
    case ExecutionStatus.Running: execution = <svg className="sidebar-status-glyph sidebar-status-running" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8"><circle cx="8" cy="8" r="5.25" opacity=".28"/><path d="M8 2.75A5.25 5.25 0 0 1 13.25 8"/></svg>; break;
    case ExecutionStatus.Succeeded: execution = <svg className="sidebar-status-glyph" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8"><path d="m3 8 3.2 3.2L13 4.8"/></svg>; break;
    case ExecutionStatus.Failed: execution = <svg className="sidebar-status-glyph" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M8 2.5 14 13H2z"/><path d="M8 6v3.3M8 11.2v.1"/></svg>; break;
    case ExecutionStatus.Stopped: execution = <svg className="sidebar-status-glyph" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5"><rect x="3" y="3" width="10" height="10" rx="1"/></svg>; break;
    default: execution = <span className="sidebar-status-unknown">?</span>;
  }
  let archiveGlyph: ReactNode = null;
  if (archive === ArchiveStatus.Archiving) archiveGlyph = <svg className="sidebar-status-glyph sidebar-archive-glyph" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5"><circle cx="8" cy="8" r="5.5"/><path d="M8 4.5V8l2.5 1.5"/></svg>;
  else if (archive === ArchiveStatus.Archived) archiveGlyph = <svg className="sidebar-status-glyph sidebar-archive-glyph" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M2.5 5h11v8h-11zM2 2.5h12v2.5H2zM6 8h4"/></svg>;
  else if (archive !== ArchiveStatus.Active) archiveGlyph = <span className="sidebar-status-unknown sidebar-archive-glyph">?</span>;
  return <span className="sidebar-statuses" data-outcome={outcome} aria-hidden="true">{execution}{archiveGlyph}</span>;
}

function SessionRow({ row, selected, open }: { row: NavigationRow; selected: boolean; open: (id: string, sidechatParent?:string, name?:string) => void }) {
  useLocale();
  const tooltipId = useId();
  const element = useRef<HTMLButtonElement>(null);
  const hover = useSessionHover(element);
  const actionMenuOpen = useSessionActionMenuOpen();
  const title = row.name;
  const outcome = row.outcome;
  const archive = row.archive;
  const workspace = workspaceLabel(row.workspace);
  const titlePresentation = row.title;
  const titleState = titlePresentation?.label;
  const titleStateDescription = titlePresentation ? [titleState, titlePresentation.detail].filter(Boolean).join(". ") : "";
  const titleStateSummary = titlePresentation ? [titleState?.replace(/^Title /, "").replace(/^[a-z]/, (letter) => letter.toUpperCase()), titlePresentation.shortDetail].filter(Boolean).join(" · ") : "";
  const description = copy("sidebar.sentence.407326d462c1", { v0: workspace, v1: title, v2: executionLabel(outcome), v3: archiveLabel(archive), v4: workspace, v5: titleStateDescription ? ` ${titleStateDescription}.` : "" });
  const workspaceIcon = row.workspace === Workspace.Worktree ? "branch" : row.workspace === Workspace.Local ? "computer" : row.workspace === Workspace.GeneralChat ? "chat" : "unknown";
  return <div className="sidebar-session-container">
    <button ref={element} type="button" className="sidebar-session-row" data-session-id={row.id} aria-current={selected ? "true" : undefined} aria-label={description} aria-describedby={tooltipId} onPointerEnter={actionMenuOpen ? undefined : hover.onPointerEnter} onPointerLeave={hover.onPointerLeave} onFocus={actionMenuOpen ? undefined : hover.onFocus} onBlur={hover.onBlur} onClick={() => { hover.dismiss(); if(row.sidechatParent)open(row.id,row.sidechatParent,row.name);else open(row.id); }}>
      <Icon name={workspaceIcon} className="sidebar-workspace-icon" />
      <span className="sidebar-session-title">{title}</span>
      {titleStateSummary ? <span className="sidebar-session-title-state">{titleStateSummary}</span> : null}
      <StatusGlyph outcome={outcome} archive={archive} />
    </button>
    <SessionRowActions id={row.id} descriptionId={tooltipId} dismissHover={hover.dismiss} />
    <span className="sidebar-sr-only" id={tooltipId}>{description}</span>
    {hover.visible ? <SessionHoverCard hover={hover}>
      <p className="sidebar-session-card-title">{title}</p>
      <p className="sidebar-session-card-workspace"><Icon name={workspaceIcon} className="sidebar-workspace-icon" />{workspace}</p>
      <dl className="sidebar-session-card-states">
        <dt>{copy("sidebar.hover.execution")}</dt><dd><span className="sidebar-session-card-badge"><StatusGlyph outcome={outcome} archive={ArchiveStatus.Active} />{cardExecutionLabel(outcome)}</span></dd>
        <dt>{copy("sidebar.hover.archive")}</dt><dd><span className="sidebar-session-card-badge">{cardArchiveLabel(archive)}</span></dd>
      </dl>
      {titlePresentation ? <div className="sidebar-session-card-title-state"><p>{titleState}</p>{titlePresentation.detail ? <p>{titlePresentation.detail}</p> : null}</div> : null}
    </SessionHoverCard> : null}
  </div>;
}

function ProjectSessions({ projectId, label, fallback = false, fallbackRows, home, includeArchived, selected, open, active, root }: {
  projectId: string; label: string; fallback?: boolean; fallbackRows: NavigationRow[]; home: HomeNavigation; includeArchived: boolean; selected: string; open: (id: string, sidechatParent?:string, name?:string) => void; active: boolean; root: RefObject<HTMLDivElement | null>;
}) {
  useLocale();
  const sessions = useNavigationQuery(home.project(projectId), HomeScope.Sessions, projectId, includeArchived, active && !fallback);
  const rows = fallback || !sessions.loaded ? fallbackRows : sessions.rows;
  return <div className="sidebar-project-sessions">
    {!fallback ? <QueryProblem query={sessions} label={copy("sidebar.sessions_f70c94", { v0: label })} retryLabel={copy("sidebar.retrySessions_66ea21", { v0: label })} /> : null}
    {fallback ? <p className="sidebar-fallback-explanation">{copy("sidebar.projectDetailsAreNotInThe_ba6cdd")}</p> : null}
    {!fallback && !sessions.loaded && !sessions.error ? <p className="sidebar-query-state" role="status"><LocalizedText id="sidebar.loadingSessions_bd5fbc" components={{ s0: <>{label}</> }} /></p> : null}
    {rows.map((row) => <SessionRow key={row.id} row={row} selected={selected === row.id} open={open} />)}
    {!fallback && sessions.loaded && !sessions.error && rows.length === 0 && !sessions.nextPageToken ? <p className="sidebar-empty">{copy("sidebar.noConversationsLoaded_b94bd7")}</p> : null}
    <ScrollContinuation showInitial={false} showErrors={false} query={sessions} label={copy("sidebar.sessions_f70c94", { v0: label })} root={root} active={active && !fallback} />
  </div>;
}

function ProjectGroup({ projectId, label, fallback = false, fallbackRows = [], expanded, toggle, newSession, projectSelectionBlocked, home, includeArchived, selected, open, active, root }: {
  projectId: string; label: string; fallback?: boolean; fallbackRows?: NavigationRow[]; expanded: boolean; toggle: () => void; home: HomeNavigation; includeArchived: boolean; selected: string; open: (id: string, sidechatParent?:string, name?:string) => void; active: boolean; root: RefObject<HTMLDivElement | null>;
  newSession: (projectId: string) => void; projectSelectionBlocked: boolean;
}) {
  const disclosureContentId1 = useId();
  useLocale();
  return <section className={`sidebar-project-group${fallback ? "" : " has-new-session"}`} data-project-id={projectId}>
    <DisclosureButton aria-controls={disclosureContentId1} density={DisclosureDensity.Compact} type="button" className="sidebar-project-row" title={label} aria-label={copy("sidebar.projectId_656c43", { v0: label, v1: projectId })} aria-expanded={expanded} onClick={toggle}>
      <Icon name="folder" className="sidebar-folder-icon" /><span className="sidebar-project-title">{label}</span><span className="sidebar-project-tooltip" aria-hidden="true">{label}</span>
    </DisclosureButton>
    {!fallback ? <button type="button" className="sidebar-project-new-session" title={copy("sidebar.newSessionInProject", { v0: label })} aria-label={copy("sidebar.newSessionInProjectId", { v0: label, v1: projectId })} disabled={projectSelectionBlocked} onClick={(event) => { event.currentTarget.focus(); newSession(projectId); }}><Icon name="plus" /></button> : null}
    {!fallback ? <ProjectSettingsMenu projectId={projectId} label={label} active={active} /> : null}
    <DisclosureContent id={disclosureContentId1} hidden={!expanded}>{expanded ? <ProjectSessions projectId={projectId} label={label} fallback={fallback} fallbackRows={fallbackRows} home={home} includeArchived={includeArchived} selected={selected} open={open} active={active} root={root} /> : null}</DisclosureContent>
  </section>;
}

export function Sidebar({ openCommandMenu, surface, selectedSessionId, serverPresentation, connectionReady = true, homeActive = true, navigate, navigateHeader = navigate, openSession, newSession, newGeneralChat, newProject, projectSelectionBlocked = false, openSettings, setContextTarget = () => undefined, drawerOpen = false, setDrawerOpen = () => undefined }: {
  openCommandMenu?: () => void; surface: Surface; selectedSessionId: string; serverPresentation?: ServerPresentation; connectionReady?: boolean; homeActive?: boolean; navigate: (surface: Surface) => void; navigateHeader?: (surface: Surface.Inbox | Surface.Search) => void; openSession: (id: string, sidechatParent?:string, name?:string) => void; newSession: (projectId?: string) => void; newGeneralChat: () => void; newProject: () => void; projectSelectionBlocked?: boolean; openSettings: (destination?: SettingsNavigationEntry) => void;
  setContextTarget?: (target: HTMLElement | null) => void; drawerOpen?: boolean; setDrawerOpen?: (open: boolean) => void;
}) {
  const disclosureContentId3 = useId();
  useLocale();
  const [compact, setCompact] = useState(false);
  const drawer = useRef<HTMLDialogElement>(null);
  const rail = useRef<HTMLElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const modalDrawer = useRef(false);
  const scrollSurface = useRef(surface);
  const surfaceScroll = useRef(new Map<Surface, number>());
  const [home] = useState(() => new HomeNavigation());
  const [visible, setVisible] = useState(() => window.document.visibilityState !== "hidden");
  const [optionsOpen, setOptionsOpen] = useState(false);
  const optionsButton = useRef<HTMLButtonElement>(null);
  const optionsPopup = useRef<HTMLDivElement>(null);
  const [includeArchived, setIncludeArchived] = useState(false);
  const [expandedProjects, setExpandedProjects] = useState<ReadonlySet<string>>(() => new Set());
  const [collapsedFallbacks, setCollapsedFallbacks] = useState<ReadonlySet<string>>(() => new Set());
  const [generalExpanded, setGeneralExpanded] = useState(true);
  const [newProjectTooltip, setNewProjectTooltip] = useState<TooltipPosition>();
  const newProjectButton = useRef<HTMLButtonElement>(null);
  const newProjectPointerInside = useRef(false);
  const newProjectFocused = useRef(false);
  const sessionNavigation = surface === Surface.Sessions || surface === Surface.NewSession || surface === Surface.NewGeneralChat;
  const active = sessionNavigation && homeActive && visible && (!compact || drawerOpen);
  useEffect(() => {
    if (sessionNavigation) return;
    // The Home tooltip is portaled outside its hidden source controls.
    newProjectPointerInside.current = false;
    newProjectFocused.current = false;
    setNewProjectTooltip(undefined);
  }, [sessionNavigation]);
  const projects = useNavigationQuery(home.catalog, HomeScope.Catalog, "", false, active);
  const sessions = useNavigationQuery(home.global, HomeScope.Sessions, "", includeArchived, active);
  useEffect(() => {
    const update = () => setVisible(window.document.visibilityState !== "hidden");
    window.document.addEventListener("visibilitychange", update);
    return () => window.document.removeEventListener("visibilitychange", update);
  }, []);
  useEffect(() => {
    if (!optionsOpen) return;
    optionsPopup.current?.querySelector<HTMLInputElement>("input")?.focus();
    const outside = (event: PointerEvent) => {
      if (!optionsPopup.current?.contains(event.target as Node) && !optionsButton.current?.contains(event.target as Node)) setOptionsOpen(false);
    };
    window.document.addEventListener("pointerdown", outside);
    return () => window.document.removeEventListener("pointerdown", outside);
  }, [optionsOpen]);
  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
  const serverName = serverPresentation?.kind === ServerPresentationKind.Saved ? serverPresentation.name : copy("sidebar.extra.26f9f95a152f");
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(max-width: 759px)");
    const update = () => setCompact(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  useLayoutEffect(() => {
    const element = drawer.current;
    if (!element) return;
    if (compact) {
      if (drawerOpen) {
        if (element.open && !modalDrawer.current) element.close();
        if (!element.open) {
          element.showModal();
          modalDrawer.current = true;
        }
      } else {
        if (element.open) element.close();
        modalDrawer.current = false;
        element.removeAttribute("open");
      }
    } else {
      const wasModal = modalDrawer.current;
      if (wasModal && element.open) element.close();
      modalDrawer.current = false;
      element.setAttribute("open", "");
      if (wasModal) {
        setDrawerOpen(false);
        requestAnimationFrame(() => (drawer.current?.querySelector<HTMLElement>("[aria-current='page']") ?? rail.current?.querySelector<HTMLElement>("[aria-current='page']"))?.focus());
      }
    }
  }, [compact, drawerOpen, setDrawerOpen]);
  useLayoutEffect(() => {
    const container = list.current;
    const target = sessionNavigation ? Surface.Sessions : surface;
    if (!container || scrollSurface.current === target) return;
    surfaceScroll.current.set(scrollSurface.current, container.scrollTop);
    container.scrollTop = surfaceScroll.current.get(target) ?? 0;
    scrollSurface.current = target;
  }, [surface]);
  const projectRows = projects.rows;
  const knownProjectIds = useMemo(() => new Set(projectRows.map((row) => row.id)), [projectRows]);
  const globalGroups = useMemo(() => {
    const groups = new Map<string, NavigationRow[]>();
    for (const row of sessions.rows) {
      const group = groups.get(row.projectId) ?? [];
      group.push(row);
      groups.set(row.projectId, group);
    }
    return groups;
  }, [sessions.rows]);
  const fallbackGroups = useMemo(() => new Map([...globalGroups].filter(([id]) => id && !knownProjectIds.has(id))), [knownProjectIds, globalGroups]);
  const generalRows = useMemo(() => sessions.rows.filter((row) => !row.projectId), [sessions.rows]);
  const showNewProjectTooltip = () => {
    const rect = newProjectButton.current?.getBoundingClientRect();
    if (!rect) return;
    const width = Math.min(120, window.innerWidth - 16);
    const left = rect.right + 6 + width <= window.innerWidth - 8 ? rect.right + 6 : Math.max(8, rect.left - width - 6);
    const top = rect.bottom + 6 + 32 < window.innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - 38);
    setNewProjectTooltip({ left, top });
  };
  const hideNewProjectTooltipWhenInactive = () => {
    if (!newProjectPointerInside.current && !newProjectFocused.current) setNewProjectTooltip(undefined);
  };
  useEffect(() => {
    if (!newProjectTooltip) return;
    const dismiss = () => setNewProjectTooltip(undefined);
    window.document.addEventListener("scroll", dismiss, true);
    window.addEventListener("resize", dismiss);
    return () => { window.document.removeEventListener("scroll", dismiss, true); window.removeEventListener("resize", dismiss); };
  }, [newProjectTooltip]);
  const previousFallbacks = useRef(new Set<string>());
  useLayoutEffect(() => {
    const migrated = [...previousFallbacks.current].filter((id) => knownProjectIds.has(id) && !collapsedFallbacks.has(id));
    if (migrated.length) setExpandedProjects((current) => {
      const next = new Set(current);
      for (const id of migrated) next.add(id);
      while (next.size > 100) next.delete(next.values().next().value!);
      return next;
    });
    previousFallbacks.current = new Set(fallbackGroups.keys());
  }, [knownProjectIds, fallbackGroups, collapsedFallbacks]);
  const toggleProject = (id: string) => setExpandedProjects((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else next.add(id);
    while (next.size > 100) next.delete(next.values().next().value!);
    return next;
  });
  const toggleFallback = (id: string) => setCollapsedFallbacks((current) => {
    const next = new Set(current);
    if (next.has(id)) next.delete(id); else next.add(id);
    while (next.size > 50) next.delete(next.values().next().value!);
    return next;
  });
  const archiveChanged = (value: boolean) => {
    setIncludeArchived(value);
    home.resetSessions();
  };

  const openShortcutHelp = useShortcutHelp();
  const commandAria = useGlobalShortcutAria(ShortcutId.CommandMenu);
  const helpAria = useGlobalShortcutAria(ShortcutId.Help);
  const newSessionAria = useGlobalShortcutAria(ShortcutId.NewSession);
  const chooseSession = (id: string, parent?:string, name?:string) => { if(parent)openSession(id,parent,name);else openSession(id); setDrawerOpen(false); };
  const chooseNewSession = (projectId?: string) => { newSession(projectId); setDrawerOpen(false); };
  return <SessionRowActionsProvider active={active}><SessionHoverProvider enabled={active} scope={surface}><aside className={`sidebar${surface === Surface.PullRequests ? " sidebar-pull-requests" : ""}`} aria-label={copy("sidebar.applicationSidebar_7e4842")}>
    <nav ref={rail} className="sidebar-rail" aria-label={copy("sidebar.primaryNavigation_e1bfe7")}>
      <SidebarButton label={copy("sidebar.sessions_6fa3cb")} icon="sessions" current={sessionNavigation} onClick={() => navigate(Surface.Sessions)} />
      <SidebarButton label={copy("sidebar.pullRequests_d9e3f2")} icon="pull-requests" current={surface === Surface.PullRequests} onClick={() => navigate(Surface.PullRequests)} />
      <SidebarButton label={copy("sidebar.usage_8d5982")} icon="usage" current={surface === Surface.Usage} onClick={() => navigate(Surface.Usage)} />
      <SidebarButton label={copy("sidebar.schedules_221ff1")} icon="schedules" current={surface === Surface.Schedules} onClick={() => navigate(Surface.Schedules)} />
      <SidebarButton label={copy("sidebar.activity_38da15")} icon="activity" current={surface === Surface.Activity} onClick={() => navigate(Surface.Activity)} />
      <span className="sidebar-rail-spacer" />
      <SubscriptionRail enabled={connectionReady && !drawerOpen} manage={openSettings} focusFallback={() => rail.current?.querySelector<HTMLButtonElement>(".sidebar-rail-button")?.focus()} />
      <button type="button" className="sidebar-rail-button" aria-label={copy("command-menu.title")} aria-keyshortcuts={commandAria} aria-haspopup="dialog" onClick={openCommandMenu}><Icon name="search" /><span className="sidebar-rail-tooltip" aria-hidden="true">{copy("command-menu.title")} <kbd>{commandAria.startsWith("Meta") ? "⌘ K" : "Ctrl K"}</kbd></span></button>
      <button type="button" className="sidebar-rail-button" aria-label={copy("shortcuts.title")} aria-keyshortcuts={helpAria} aria-haspopup="dialog" onClick={openShortcutHelp}><Icon name="help" /><span className="sidebar-rail-tooltip" aria-hidden="true">{copy("shortcuts.title")}</span></button>
      <SidebarButton label={copy("sidebar.settings_74a883")} icon="settings" current={surface === Surface.Settings} onClick={(event) => { event.currentTarget.focus(); openSettings(); }} />
    </nav>
    <dialog ref={drawer} role={compact ? "dialog" : "region"} className={`sidebar-pane-dialog${compact && drawerOpen ? " is-drawer" : ""}`} aria-label={compact ? copy("sidebar.delidevNavigation_a550af") : undefined} onCancel={(event) => { event.preventDefault(); setDrawerOpen(false); }} onClose={() => { modalDrawer.current = false; }}>
    <div className={`sidebar-pane${sessionNavigation ? " is-home" : ""}`}>
      <header className="sidebar-header">
        <h1>{copy("sidebar.delidev_44fcad")}</h1>
        {sessionNavigation ? <div className="sidebar-header-actions">
          <button type="button" className="sidebar-header-button" aria-label={copy("sidebar.inbox_94835e")} onClick={() => navigateHeader(Surface.Inbox)}><Icon name="inbox" /></button>
          <button type="button" className="sidebar-header-button" aria-label={copy("sidebar.search_49c266")} onClick={() => navigateHeader(Surface.Search)}><Icon name="search" /></button>
        </div> : null}
      </header>
      <button type="button" className="sidebar-drawer-close" onClick={() => setDrawerOpen(false)}>{copy("sidebar.closeNavigation_99904d")}</button>
      {sessionNavigation ? <button type="button" className="sidebar-new-session" aria-keyshortcuts={newSessionAria} aria-current={surface === Surface.NewSession ? "page" : undefined} onClick={(event) => { event.currentTarget.focus(); newSession(); setDrawerOpen(false); }}><Icon name="plus" />{copy("sidebar.newSession_cffdba")}</button> : null}
      {sessionNavigation ? <button type="button" className="sidebar-new-general-chat" aria-current={surface === Surface.NewGeneralChat ? "page" : undefined} onClick={(event) => { event.currentTarget.focus(); newGeneralChat(); setDrawerOpen(false); }}><Icon name="chat-plus" />{copy("sidebar.newGeneralChat")}</button> : null}
      <div ref={list} className="sidebar-list" onScroll={(event) => surfaceScroll.current.set(sessionNavigation ? Surface.Sessions : surface, event.currentTarget.scrollTop)} aria-label={sessionNavigation ? copy("sidebar.projectAndSessionNavigation_ccbca5") : copy("sidebar.menuNavigationAndFilters_b5a21d")}>
        <div hidden={!sessionNavigation}>
        <header className="sidebar-projects-heading"><h2>{copy("sidebar.projects_04e2a9")}</h2><button ref={newProjectButton} type="button" className="sidebar-new-project-button" aria-label={copy("sidebar.newProject_a41eb2")} onPointerEnter={() => { newProjectPointerInside.current = true; showNewProjectTooltip(); }} onPointerLeave={() => { newProjectPointerInside.current = false; hideNewProjectTooltipWhenInactive(); }} onFocus={() => { newProjectFocused.current = true; showNewProjectTooltip(); }} onBlur={() => { newProjectFocused.current = false; hideNewProjectTooltipWhenInactive(); }} onClick={(event) => { event.currentTarget.focus(); setDrawerOpen(false); setNewProjectTooltip(undefined); newProject(); }}><Icon name="plus" /></button><button ref={optionsButton} type="button" className="sidebar-options-button" aria-label={copy("sidebar.projectAndConversationOptions_60b63e")} aria-haspopup="dialog" aria-expanded={optionsOpen} onClick={() => setOptionsOpen((current) => !current)}><Icon name="options" /></button>{optionsOpen ? <div ref={optionsPopup} role="dialog" aria-label={copy("sidebar.projectAndConversationOptions_60b63e")} className="sidebar-options-popup" onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setOptionsOpen(false); optionsButton.current?.focus(); } }}><label className="sidebar-archived-filter"><input type="checkbox" checked={includeArchived} onChange={(event) => archiveChanged(event.target.checked)} />{copy("sidebar.includeArchived_b6c334")}</label></div> : null}</header>
        {sessionNavigation && newProjectTooltip ? createPortal(<div className="sidebar-action-tooltip" role="tooltip" aria-hidden="true" style={{ left: newProjectTooltip.left, top: newProjectTooltip.top }}>{copy("sidebar.newProject_a41eb2")}</div>, window.document.body) : null}
        {includeArchived ? <p className="sidebar-archive-indicator">{copy("sidebar.archivedIncluded_5c65cb")}</p> : null}
        <QueryProblem query={projects} label={copy("sidebar.projects_2577c0")} retryLabel={copy("sidebar.retryProjectCatalog_6fc560")} />
        <QueryProblem query={sessions} label={copy("sidebar.sessions_1225ae")} retryLabel={copy("sidebar.retryGlobalSessions_4d93c1")} />
        {!projects.loaded && !projects.error ? <p className="sidebar-query-state" role="status">{copy("sidebar.loadingProjects_6970a1")}</p> : null}
        {projects.loaded && !projects.error && projectRows.length === 0 && !projects.nextPageToken ? <div className="sidebar-empty"><p>{copy("sidebar.noProjectsLoaded_9b9e01")}</p><button type="button" onClick={(event) => { event.currentTarget.focus(); setDrawerOpen(false); setNewProjectTooltip(undefined); newProject(); }}>{copy("sidebar.createAProject_c52af0")}</button></div> : null}
        {[...projectRows.map((project) => ({ id: project.id, label: project.name, fallback: false, rows: globalGroups.get(project.id) })), ...[...fallbackGroups].map(([id, rows]) => ({ id, label: copy("sidebar.sentence.7436726e0559", { v0: id }), fallback: true, rows }))].map((group) => <ProjectGroup key={group.id} projectId={group.id} label={group.label} fallback={group.fallback} fallbackRows={group.rows} expanded={group.fallback ? !collapsedFallbacks.has(group.id) : expandedProjects.has(group.id) || previousFallbacks.current.has(group.id) && !collapsedFallbacks.has(group.id)} toggle={() => group.fallback ? toggleFallback(group.id) : toggleProject(group.id)} newSession={chooseNewSession} projectSelectionBlocked={projectSelectionBlocked} home={home} includeArchived={includeArchived} selected={selectedSessionId} open={chooseSession} active={active} root={list} />)}
        <ScrollContinuation showInitial={false} showErrors={false} query={projects} label={copy("sidebar.projects_2577c0")} root={list} active={active} />
        <section className="sidebar-project-group sidebar-general-chat has-new-session">
          <DisclosureButton aria-controls={disclosureContentId3} density={DisclosureDensity.Compact} type="button" className="sidebar-project-row sidebar-general-chat-heading" aria-expanded={generalExpanded} onClick={() => setGeneralExpanded((current) => !current)}><Icon name="chat" className="sidebar-folder-icon" /><span className="sidebar-project-title">{copy("sidebar.generalChat_f634bc")}</span></DisclosureButton>
          <button type="button" className="sidebar-project-new-session" title={copy("sidebar.newGeneralChat")} aria-label={copy("sidebar.newGeneralChat")} onClick={(event) => { event.currentTarget.focus(); newGeneralChat(); setDrawerOpen(false); }}><Icon name="plus" /></button>
          <DisclosureContent id={disclosureContentId3} hidden={!generalExpanded}>{generalExpanded ? <>
            {!sessions.loaded && !sessions.error ? <p className="sidebar-query-state" role="status">{copy("sidebar.loadingSessions_c4141f")}</p> : null}
            {generalRows.map((row) => <SessionRow key={row.id} row={row} selected={selectedSessionId === row.id} open={chooseSession} />)}
            {sessions.loaded && !sessions.error && generalRows.length === 0 && !sessions.nextPageToken ? <p className="sidebar-empty">{copy("sidebar.noConversationsLoaded_b94bd7")}</p> : null}
            <ScrollContinuation showInitial={false} showErrors={false} query={sessions} label={copy("sidebar.sessions_1225ae")} root={list} active={active && generalExpanded} />
          </> : null}</DisclosureContent>
        </section>
        </div>
        <div className="sidebar-surface-outlet" ref={setContextTarget} />
      </div>
      <footer className="sidebar-footer">
        <p role="status">{serverName} · {status.error || !connectionReady || status.data?.stopping ? copy("sidebar.disconnectedPreviousDataMayBeStale_359e28") : status.data ? copy("sidebar.connected_229655") : status.isPending ? copy("sidebar.connecting_72021e") : copy("sidebar.disconnected_04dfac")}</p>
      </footer>
    </div>
    </dialog>
  </aside></SessionHoverProvider></SessionRowActionsProvider>;
}
