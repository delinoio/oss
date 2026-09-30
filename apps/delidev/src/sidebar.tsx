import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type RefObject, type MouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";

import { useQuery } from "@connectrpc/connect-query";
import { FailureCode, SystemQuery } from "@delinoio/delidev-api-client";
import { Workspace, workspaceNames } from "./documents";
import { Surface } from "./views";
import { SettingsEntryDestination } from "./settings";
import { HomeNavigation, ReadStage, type NavigationRow } from "./home-navigation";
import { HomeScope, useNavigationQuery } from "./home-navigation-query";
import { ServerPresentationKind, type ServerPresentation } from "./server-presentation";

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

function Icon({ name, className = "" }: { name: string; className?: string }) {
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
    case "unknown": return <svg {...common}><circle cx="12" cy="12" r="9"/><path d="M9.8 9a2.3 2.3 0 1 1 4.3 1.2c-.9 1.1-2.1 1.2-2.1 3M12 17h.01"/></svg>;
    case "options": return <svg {...common}><circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/></svg>;
    case "server": return <svg {...common}><rect x="3" y="3" width="18" height="8" rx="2"/><rect x="3" y="13" width="18" height="8" rx="2"/><path d="M7 7h.01M7 17h.01M12 7h5M12 17h5"/></svg>;
    case "plus": return <svg {...common}><path d="M12 5v14M5 12h14"/></svg>;
    case "chevron": return <svg {...common}><path d="m8 10 4 4 4-4"/></svg>;
    default: return null;
  }
}

function SidebarButton({ label, icon, current, onClick, className = "" }: { label: string; icon: string; current?: boolean; onClick: (event: MouseEvent<HTMLButtonElement>) => void; className?: string }) {
  return <button type="button" aria-label={label} aria-current={current ? "page" : undefined} className={`sidebar-rail-button ${className}`} onClick={onClick}>
    <Icon name={icon} /><span className="sidebar-rail-tooltip" aria-hidden="true">{label}</span>
  </button>;
}

type NavigationQuery = ReturnType<typeof useNavigationQuery>;
function QueryProblem({ query, label, retryLabel }: { query: NavigationQuery; label: string; retryLabel: string }) {
  if (!query.error) return null;
  const { failure, stalled } = query.error;
  const reload = failure.code === FailureCode.CursorExpired || stalled;
  const message = reload ? `${label}: ${stalled ? "The list continuation did not advance." : "The list cursor expired."}` : query.loaded ? `Could not ${query.error.stage === ReadStage.Additional ? "load more" : "refresh"} ${label}. Previous data is shown.` : failure.code === FailureCode.PermissionDenied ? `You do not have permission to view ${label}.` : `Could not connect to load ${label}.`;
  return <div className="sidebar-query-problem">
    <span role="status">{message}{failure.correlationId ? ` Correlation: ${failure.correlationId}` : ""}</span>
    <button type="button" aria-label={reload ? `Reload ${label} list` : retryLabel} disabled={Boolean(query.loading)} onClick={reload ? query.reload : query.retry}>{reload ? "Reload list" : "Retry"}</button>
  </div>;
}

function Continuation({ query, label, root, active }: { query: NavigationQuery; label: string; root: RefObject<HTMLDivElement | null>; active: boolean }) {
  const anchor = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const container = root.current, element = anchor.current;
    if (!container || !element || !active || !query.nextPageToken || query.error || query.loading) return;
    const check = () => {
      if (container.clientHeight <= 0 || window.document.visibilityState === "hidden") return;
      const bounds = container.getBoundingClientRect(), position = element.getBoundingClientRect();
      if (position.top <= bounds.bottom + 96 && position.bottom >= bounds.top) query.append();
    };
    const observer = typeof IntersectionObserver === "function" ? new IntersectionObserver(check, { root: container, rootMargin: "0px 0px 96px 0px" }) : undefined;
    observer?.observe(element);
    const resize = typeof ResizeObserver === "function" ? new ResizeObserver(check) : undefined;
    resize?.observe(container);
    container.addEventListener("scroll", check, { passive: true });
    window.addEventListener("resize", check);
    check();
    return () => { observer?.disconnect(); resize?.disconnect(); container.removeEventListener("scroll", check); window.removeEventListener("resize", check); };
  }, [query, root, active]);
  return <div ref={anchor} className="sidebar-continuation" data-continuation={label}>{query.loading === ReadStage.Additional ? <span role="status">Loading more {label}…</span> : null}</div>;
}

function workspaceLabel(raw: string): string {
  if (Object.values(Workspace).includes(raw as Workspace)) return workspaceNames[raw as Workspace];
  return raw ? `Unknown workspace (${raw})` : "Unknown workspace";
}

function executionLabel(raw: string): string {
  return raw || "unknown";
}

function archiveLabel(raw: string): string {
  return raw || "unknown";
}

function StatusGlyph({ outcome, archive }: { outcome: string; archive: string }) {
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
  return <span className="sidebar-statuses" aria-hidden="true">{execution}{archiveGlyph}</span>;
}

function SessionRow({ row, selected, open }: { row: NavigationRow; selected: boolean; open: (id: string) => void }) {
  const tooltipId = useId();
  const element = useRef<HTMLButtonElement>(null);
  const [tooltip, setTooltip] = useState<TooltipPosition>();
  const title = row.name;
  const outcome = row.outcome;
  const archive = row.archive;
  const workspace = workspaceLabel(row.workspace);
  const titlePresentation = row.title;
  const titleState = titlePresentation?.label;
  const titleStateDescription = titlePresentation ? [titleState, titlePresentation.detail].filter(Boolean).join(". ") : "";
  const titleStateSummary = titlePresentation ? [titleState?.replace(/^Title /, "").replace(/^[a-z]/, (letter) => letter.toUpperCase()), titlePresentation.shortDetail].filter(Boolean).join(" · ") : "";
  const description = `Session: ${workspace} ${title}. Execution state: ${executionLabel(outcome)}. Archive state: ${archiveLabel(archive)}. Workspace: ${workspace}.${titleStateDescription ? ` ${titleStateDescription}.` : ""}`;
  const showTooltip = () => {
    const rect = element.current?.getBoundingClientRect();
    if (!rect) return;
    const width = Math.min(320, window.innerWidth - 16);
    const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
    const top = rect.bottom + 6 + 84 < window.innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - 84);
    setTooltip({ left, top });
  };
  useEffect(() => {
    if (!tooltip) return;
    const dismiss = () => setTooltip(undefined);
    window.document.addEventListener("scroll", dismiss, true);
    window.addEventListener("resize", dismiss);
    return () => { window.document.removeEventListener("scroll", dismiss, true); window.removeEventListener("resize", dismiss); };
  }, [tooltip]);
  const workspaceIcon = row.workspace === Workspace.Worktree ? "branch" : row.workspace === Workspace.Local ? "computer" : row.workspace === Workspace.GeneralChat ? "chat" : "unknown";
  return <>
    <button ref={element} type="button" className="sidebar-session-row" data-session-id={row.id} aria-current={selected ? "true" : undefined} aria-label={description} aria-describedby={tooltipId} onPointerEnter={showTooltip} onPointerLeave={() => setTooltip(undefined)} onFocus={showTooltip} onBlur={() => setTooltip(undefined)} onClick={() => open(row.id)}>
      <Icon name={workspaceIcon} className="sidebar-workspace-icon" />
      <span className="sidebar-session-title">{title}</span>
      {titleStateSummary ? <span className="sidebar-session-title-state" title={titlePresentation?.detail}>{titleStateSummary}</span> : null}
      <StatusGlyph outcome={outcome} archive={archive} />
    </button>
    <span className="sidebar-sr-only" id={tooltipId}>{description}</span>
    {tooltip ? createPortal(<div className="sidebar-session-tooltip" role="tooltip" style={{ left: tooltip.left, top: tooltip.top }}>{description}</div>, window.document.body) : null}
  </>;
}

function ProjectSessions({ projectId, label, fallback = false, fallbackRows, home, includeArchived, selected, open, active, root }: {
  projectId: string; label: string; fallback?: boolean; fallbackRows: NavigationRow[]; home: HomeNavigation; includeArchived: boolean; selected: string; open: (id: string) => void; active: boolean; root: RefObject<HTMLDivElement | null>;
}) {
  const sessions = useNavigationQuery(home.project(projectId), HomeScope.Sessions, projectId, includeArchived, active && !fallback);
  const rows = fallback || !sessions.loaded ? fallbackRows : sessions.rows;
  return <div className="sidebar-project-sessions">
    {!fallback ? <QueryProblem query={sessions} label={`${label} sessions`} retryLabel={`Retry ${label} sessions`} /> : null}
    {fallback ? <p className="sidebar-fallback-explanation">Project details are not in the loaded project catalog.</p> : null}
    {!fallback && !sessions.loaded && !sessions.error ? <p className="sidebar-query-state" role="status">Loading {label} sessions…</p> : null}
    {rows.map((row) => <SessionRow key={row.id} row={row} selected={selected === row.id} open={open} />)}
    {!fallback && sessions.loaded && !sessions.error && rows.length === 0 && !sessions.nextPageToken ? <p className="sidebar-empty">No conversations loaded.</p> : null}
    <Continuation query={sessions} label={`${label} sessions`} root={root} active={active && !fallback} />
  </div>;
}

function ProjectGroup({ projectId, label, fallback = false, fallbackRows = [], expanded, toggle, home, includeArchived, selected, open, active, root }: {
  projectId: string; label: string; fallback?: boolean; fallbackRows?: NavigationRow[]; expanded: boolean; toggle: () => void; home: HomeNavigation; includeArchived: boolean; selected: string; open: (id: string) => void; active: boolean; root: RefObject<HTMLDivElement | null>;
}) {
  return <section className="sidebar-project-group" data-project-id={projectId}>
    <button type="button" className="sidebar-project-row" title={label} aria-label={`${label}. Project ID: ${projectId}`} aria-expanded={expanded} onClick={toggle}>
      <Icon name="folder" className="sidebar-folder-icon" /><span className="sidebar-project-title">{label}</span><span className="sidebar-project-tooltip" aria-hidden="true">{label}</span><Icon name="chevron" className={`sidebar-disclosure ${expanded ? "is-expanded" : ""}`} />
    </button>
    {expanded ? <ProjectSessions projectId={projectId} label={label} fallback={fallback} fallbackRows={fallbackRows} home={home} includeArchived={includeArchived} selected={selected} open={open} active={active} root={root} /> : null}
  </section>;
}

export function Sidebar({ surface, selectedSessionId, localServer, serverPresentation, homeActive = true, navigate, openSession, newSession, openSettings, setContextTarget = () => undefined, drawerOpen = false, setDrawerOpen = () => undefined }: {
  surface: Surface; selectedSessionId: string; localServer?: ReactNode; serverPresentation?: ServerPresentation; homeActive?: boolean; navigate: (surface: Surface) => void; openSession: (id: string) => void; newSession: () => void; openSettings: (destination?: SettingsEntryDestination) => void;
  setContextTarget?: (target: HTMLElement | null) => void; drawerOpen?: boolean; setDrawerOpen?: (open: boolean) => void;
}) {
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
  const [serverExpanded, setServerExpanded] = useState(false);
  const [includeArchived, setIncludeArchived] = useState(false);
  const [expandedProjects, setExpandedProjects] = useState<ReadonlySet<string>>(() => new Set());
  const [collapsedFallbacks, setCollapsedFallbacks] = useState<ReadonlySet<string>>(() => new Set());
  const [generalExpanded, setGeneralExpanded] = useState(true);
  const [newProjectTooltip, setNewProjectTooltip] = useState<TooltipPosition>();
  const newProjectButton = useRef<HTMLButtonElement>(null);
  const newProjectPointerInside = useRef(false);
  const newProjectFocused = useRef(false);
  const sessionNavigation = surface === Surface.Sessions || surface === Surface.NewSession;
  const active = sessionNavigation && homeActive && visible && (!compact || drawerOpen);
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
  const serverName = serverPresentation?.kind === ServerPresentationKind.Saved ? serverPresentation.name : "Local server";
  const serverStatus = status.error ? "Server unavailable" : status.data ? `Server ${status.data.version}` : status.isPending ? "Connecting to server…" : "Server unavailable";
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

  const chooseSession = (id: string) => { openSession(id); setDrawerOpen(false); };
  return <aside className="sidebar" aria-label="Application sidebar">
    <nav ref={rail} className="sidebar-rail" aria-label="Primary navigation">
      <SidebarButton label="Sessions" icon="sessions" current={sessionNavigation} onClick={() => navigate(Surface.Sessions)} />
      <SidebarButton label="Pull requests" icon="pull-requests" current={surface === Surface.PullRequests} onClick={() => navigate(Surface.PullRequests)} />
      <SidebarButton label="Usage" icon="usage" current={surface === Surface.Usage} onClick={() => navigate(Surface.Usage)} />
      <SidebarButton label="Schedules" icon="schedules" current={surface === Surface.Schedules} onClick={() => navigate(Surface.Schedules)} />
      <SidebarButton label="Activity" icon="activity" current={surface === Surface.Activity} onClick={() => navigate(Surface.Activity)} />
      <span className="sidebar-rail-spacer" />
      <SidebarButton label="Settings" icon="settings" onClick={(event) => { event.currentTarget.focus(); openSettings(); }} />
    </nav>
    <dialog ref={drawer} role={compact ? "dialog" : "region"} className={`sidebar-pane-dialog${compact && drawerOpen ? " is-drawer" : ""}`} aria-label={compact ? "DeliDev navigation" : undefined} onCancel={(event) => { event.preventDefault(); setDrawerOpen(false); }} onClose={() => { modalDrawer.current = false; }}>
    <div className={`sidebar-pane${sessionNavigation ? " is-home" : ""}`}>
      <header className="sidebar-header">
        <h1>DeliDev</h1>
        <div className="sidebar-header-actions">
          <button type="button" className="sidebar-header-button" aria-label="Inbox" aria-current={surface === Surface.Inbox ? "page" : undefined} onClick={() => navigate(Surface.Inbox)}><Icon name="inbox" /></button>
          <button type="button" className="sidebar-header-button" aria-label="Search" aria-current={surface === Surface.Search ? "page" : undefined} onClick={() => navigate(Surface.Search)}><Icon name="search" /></button>
        </div>
      </header>
      <button type="button" className="sidebar-drawer-close" onClick={() => setDrawerOpen(false)}>Close navigation</button>
      {sessionNavigation ? <button type="button" className="sidebar-new-session" aria-current={surface === Surface.NewSession ? "page" : undefined} onClick={(event) => { event.currentTarget.focus(); newSession(); setDrawerOpen(false); }}><Icon name="plus" />New session</button> : null}
      <div ref={list} className="sidebar-list" onScroll={(event) => surfaceScroll.current.set(sessionNavigation ? Surface.Sessions : surface, event.currentTarget.scrollTop)} aria-label={sessionNavigation ? "Project and session navigation" : "Menu navigation and filters"}>
        <div hidden={!sessionNavigation}>
        <header className="sidebar-projects-heading"><h2>Projects</h2><button ref={newProjectButton} type="button" className="sidebar-new-project-button" aria-label="New project" onPointerEnter={() => { newProjectPointerInside.current = true; showNewProjectTooltip(); }} onPointerLeave={() => { newProjectPointerInside.current = false; hideNewProjectTooltipWhenInactive(); }} onFocus={() => { newProjectFocused.current = true; showNewProjectTooltip(); }} onBlur={() => { newProjectFocused.current = false; hideNewProjectTooltipWhenInactive(); }} onClick={(event) => { event.currentTarget.focus(); setDrawerOpen(false); openSettings(SettingsEntryDestination.NewProject); }}><Icon name="plus" /></button><button ref={optionsButton} type="button" className="sidebar-options-button" aria-label="Project and conversation options" aria-haspopup="dialog" aria-expanded={optionsOpen} onClick={() => setOptionsOpen((current) => !current)}><Icon name="options" /></button>{optionsOpen ? <div ref={optionsPopup} role="dialog" aria-label="Project and conversation options" className="sidebar-options-popup" onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setOptionsOpen(false); optionsButton.current?.focus(); } }}><label className="sidebar-archived-filter"><input type="checkbox" checked={includeArchived} onChange={(event) => archiveChanged(event.target.checked)} />Include archived</label></div> : null}</header>
        {newProjectTooltip ? createPortal(<div className="sidebar-action-tooltip" role="tooltip" aria-hidden="true" style={{ left: newProjectTooltip.left, top: newProjectTooltip.top }}>New project</div>, window.document.body) : null}
        {includeArchived ? <p className="sidebar-archive-indicator">Archived included</p> : null}
        <QueryProblem query={projects} label="projects" retryLabel="Retry project catalog" />
        {!projects.loaded && !projects.error ? <p className="sidebar-query-state" role="status">Loading projects…</p> : null}
        {projects.loaded && !projects.error && projectRows.length === 0 && !projects.nextPageToken ? <div className="sidebar-empty"><p>No projects loaded.</p><button type="button" onClick={(event) => { event.currentTarget.focus(); setDrawerOpen(false); openSettings(SettingsEntryDestination.NewProject); }}>Create a project</button></div> : null}
        {[...projectRows.map((project) => ({ id: project.id, label: project.name, fallback: false, rows: globalGroups.get(project.id) })), ...[...fallbackGroups].map(([id, rows]) => ({ id, label: `Project · ${id}`, fallback: true, rows }))].map((group) => <ProjectGroup key={group.id} projectId={group.id} label={group.label} fallback={group.fallback} fallbackRows={group.rows} expanded={group.fallback ? !collapsedFallbacks.has(group.id) : expandedProjects.has(group.id) || previousFallbacks.current.has(group.id) && !collapsedFallbacks.has(group.id)} toggle={() => group.fallback ? toggleFallback(group.id) : toggleProject(group.id)} home={home} includeArchived={includeArchived} selected={selectedSessionId} open={chooseSession} active={active} root={list} />)}
        <Continuation query={projects} label="projects" root={list} active={active} />
        <section className="sidebar-project-group sidebar-general-chat">
          <button type="button" className="sidebar-project-row sidebar-general-chat-heading" aria-expanded={generalExpanded} onClick={() => setGeneralExpanded((current) => !current)}><Icon name="chat" className="sidebar-folder-icon" /><span className="sidebar-project-title">General Chat</span><Icon name="chevron" className={`sidebar-disclosure ${generalExpanded ? "is-expanded" : ""}`} /></button>
          {generalExpanded ? <>
            <QueryProblem query={sessions} label="sessions" retryLabel="Retry global sessions" />
            {!sessions.loaded && !sessions.error ? <p className="sidebar-query-state" role="status">Loading sessions…</p> : null}
            {generalRows.map((row) => <SessionRow key={row.id} row={row} selected={selectedSessionId === row.id} open={chooseSession} />)}
            {sessions.loaded && !sessions.error && generalRows.length === 0 && !sessions.nextPageToken ? <p className="sidebar-empty">No conversations loaded.</p> : null}
            <Continuation query={sessions} label="sessions" root={list} active={active && generalExpanded} />
          </> : null}
        </section>
        </div>
        <div className="sidebar-surface-outlet" ref={setContextTarget} />
      </div>
      <footer className="sidebar-footer">
        <button type="button" hidden={!sessionNavigation} className="sidebar-server-summary" aria-label={`${serverName} ${serverStatus}`} aria-expanded={serverExpanded} aria-controls="sidebar-server-management" onClick={() => setServerExpanded((current) => !current)}><Icon name="server" /><span className="sidebar-server-label"><span>{serverPresentation?.kind === ServerPresentationKind.Saved ? serverPresentation.name : "Local server"}</span><span role="status">{status.error ? "Server unavailable" : status.data ? `Server ${status.data.version}` : status.isPending ? "Connecting to server…" : "Server unavailable"}</span></span><Icon name="chevron" className={`sidebar-disclosure ${serverExpanded ? "is-expanded" : ""}`} /></button>
        {!sessionNavigation ? <p role="status">{status.error ? "Server unavailable" : status.data ? `Server ${status.data.version}` : status.isPending ? "Connecting to server…" : "Server unavailable"}</p> : null}
        <div id="sidebar-server-management" className="sidebar-server-management" hidden={sessionNavigation && !serverExpanded}>{localServer}</div>
      </footer>
    </div>
    </dialog>
  </aside>;
}
