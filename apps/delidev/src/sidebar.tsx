import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text, Workspace, workspaceNames } from "./documents";
import { Surface } from "./views";
import { SettingsEntryDestination } from "./settings";
import { sessionTitlePresentation } from "./session-title";

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

function QueryProblem({ error, hasData, label, retryLabel, fetching, retry }: { error: unknown; hasData: boolean; label: string; retryLabel: string; fetching: boolean; retry: () => void }) {
  if (!error) return null;
  const permissionDenied = error instanceof ConnectError && error.code === Code.PermissionDenied;
  return <div className="sidebar-query-problem">
    <span role="status">{hasData ? `Could not refresh ${label}. Previous data is shown.` : permissionDenied ? `You do not have permission to view ${label}.` : `Could not connect to load ${label}.`}</span>
    <button type="button" aria-label={retryLabel} disabled={fetching} onClick={retry}>Retry</button>
  </div>;
}

function projectName(resource: Resource): string {
  return resourceName(resource);
}

function uniqueSessions(rows: Resource[]): Resource[] {
  const seen = new Set<string>();
  return rows.filter((row) => {
    if (!row.id || seen.has(row.id)) return false;
    seen.add(row.id);
    return true;
  });
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

function SessionRow({ row, selected, open }: { row: Resource; selected: boolean; open: (id: string) => void }) {
  const tooltipId = useId();
  const element = useRef<HTMLButtonElement>(null);
  const [tooltip, setTooltip] = useState<TooltipPosition>();
  const data = document(row);
  const title = resourceName(row);
  const outcome = text(data.outcome);
  const archive = text(data.archive);
  const workspace = workspaceLabel(text(data.workspace));
  const titlePresentation = sessionTitlePresentation(data);
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
  const workspaceIcon = text(data.workspace) === Workspace.Worktree ? "branch" : text(data.workspace) === Workspace.Local ? "computer" : text(data.workspace) === Workspace.GeneralChat ? "chat" : "unknown";
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

function ProjectGroup({ projectId, label, fallback = false, fallbackRows = [], expanded, toggle, page, setPage, includeArchived, selected, open, active }: {
  projectId: string; label: string; fallback?: boolean; fallbackRows?: Resource[]; expanded: boolean; toggle: () => void; page: string; setPage: (page: string) => void; includeArchived: boolean; selected: string; open: (id: string) => void; active: boolean;
}) {
  const id = projectId;
  const sessions = useQuery(SessionQuery.listSessions, { projectId: id, includeArchived, pageSize: 50, pageToken: page }, { enabled: active && expanded && !fallback, refetchInterval: active && expanded && !fallback ? 15000 : false, refetchIntervalInBackground: false });
  const rows = fallback ? uniqueSessions(fallbackRows) : uniqueSessions(sessions.data?.sessions ?? []);
  const loaded = fallback || sessions.data !== undefined;
  return <section className="sidebar-project-group" data-project-id={id}>
    <button type="button" className="sidebar-project-row" aria-label={`${label}. Project ID: ${id}`} aria-expanded={expanded} onClick={toggle}>
      <Icon name="folder" className="sidebar-folder-icon" /><span className="sidebar-project-title">{label}</span><Icon name="chevron" className={`sidebar-disclosure ${expanded ? "is-expanded" : ""}`} />
    </button>
    {fallback && expanded ? <p className="sidebar-fallback-explanation">Project details are not in the loaded project page.</p> : null}
    {expanded ? <div className="sidebar-project-sessions">
      {!fallback ? <QueryProblem error={sessions.error} hasData={loaded} label={`${label} sessions`} retryLabel={`Retry ${label} sessions`} fetching={sessions.isFetching} retry={() => { void sessions.refetch(); }} /> : null}
      {!fallback && sessions.isPending ? <p className="sidebar-query-state" role="status">Loading {label} sessions…</p> : null}
      {rows.map((row) => <SessionRow key={row.id} row={row} selected={selected === row.id} open={open} />)}
      {!fallback && sessions.data && !sessions.error && rows.length === 0 ? <p className="sidebar-empty">No sessions on this project page.</p> : null}
      {!fallback && !sessions.error && sessions.data?.nextPageToken ? <div className="sidebar-group-pager">
        <button type="button" disabled={!page || sessions.isFetching} onClick={() => setPage("")}>First page of {label} sessions</button>
        <button type="button" disabled={sessions.isFetching} onClick={() => setPage(sessions.data!.nextPageToken)}>Next page of {label} sessions</button>
      </div> : !fallback && !sessions.error && page ? <button type="button" disabled={sessions.isFetching} onClick={() => setPage("")}>First page of {label} sessions</button> : null}
    </div> : null}
  </section>;
}

export function Sidebar({ surface, selectedSessionId, navigate, openSession, newSession, openSettings, setContextTarget = () => undefined, drawerOpen = false, setDrawerOpen = () => undefined }: {
  surface: Surface; selectedSessionId: string; navigate: (surface: Surface) => void; openSession: (id: string) => void; newSession: () => void; openSettings: (destination?: SettingsEntryDestination) => void;
  setContextTarget?: (target: HTMLElement | null) => void; drawerOpen?: boolean; setDrawerOpen?: (open: boolean) => void;
}) {
  const [compact, setCompact] = useState(false);
  const drawer = useRef<HTMLDialogElement>(null);
  const rail = useRef<HTMLElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const modalDrawer = useRef(false);
  const scrollSurface = useRef(surface);
  const surfaceScroll = useRef(new Map<Surface, number>());
  const [projectsPage, setProjectsPage] = useState("");
  const [globalPage, setGlobalPage] = useState("");
  const [includeArchived, setIncludeArchived] = useState(false);
  const [expandedProjects, setExpandedProjects] = useState<ReadonlySet<string>>(() => new Set());
  const [collapsedFallbacks, setCollapsedFallbacks] = useState<ReadonlySet<string>>(() => new Set());
  const [generalExpanded, setGeneralExpanded] = useState(true);
  const [projectPages, setProjectPages] = useState<ReadonlyMap<string, string>>(() => new Map());
  const [newProjectTooltip, setNewProjectTooltip] = useState<TooltipPosition>();
  const newProjectButton = useRef<HTMLButtonElement>(null);
  const newProjectPointerInside = useRef(false);
  const newProjectFocused = useRef(false);
  const sessionNavigation = surface === Surface.Sessions || surface === Surface.NewSession;
  const projects = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.PROJECT, pageSize: 50, pageToken: projectsPage } }, { enabled: sessionNavigation });
  const sessions = useQuery(SessionQuery.listSessions, { projectId: "", includeArchived, pageSize: 50, pageToken: globalPage }, { enabled: sessionNavigation, refetchInterval: sessionNavigation ? 15000 : false, refetchIntervalInBackground: false });
  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
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
    if (!container || scrollSurface.current === surface) return;
    surfaceScroll.current.set(scrollSurface.current, container.scrollTop);
    container.scrollTop = surfaceScroll.current.get(surface) ?? 0;
    scrollSurface.current = surface;
  }, [surface]);
  const projectRows = projects.data?.resources ?? [];
  const knownProjectIds = useMemo(() => new Set(projectRows.map((row) => row.id)), [projectRows]);
  const fallbackGroups = useMemo(() => {
    const groups = new Map<string, Resource[]>();
    for (const row of sessions.data?.sessions ?? []) {
      if (!row.projectId || knownProjectIds.has(row.projectId)) continue;
      const group = groups.get(row.projectId) ?? [];
      group.push(row);
      groups.set(row.projectId, group);
    }
    return groups;
  }, [knownProjectIds, sessions.data?.sessions]);
  const generalRows = useMemo(() => uniqueSessions((sessions.data?.sessions ?? []).filter((row) => !row.projectId)), [sessions.data?.sessions]);
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
  const setProjectPage = (id: string, page: string) => setProjectPages((current) => {
    const next = new Map(current);
    if (page) next.set(id, page); else next.delete(id);
    while (next.size > 50) next.delete(next.keys().next().value!);
    return next;
  });
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
    setGlobalPage("");
    setProjectPages(new Map());
  };

  const chooseSession = (id: string) => { openSession(id); setDrawerOpen(false); };
  return <aside className={`sidebar${surface === Surface.PullRequests ? " sidebar-pull-requests" : ""}`} aria-label="Application sidebar">
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
    <div className="sidebar-pane">
      <header className="sidebar-header">
        <h1>DeliDev</h1>
        <div className="sidebar-header-actions">
          <button type="button" className="sidebar-header-button" aria-label="Inbox" aria-current={surface === Surface.Inbox ? "page" : undefined} onClick={() => navigate(Surface.Inbox)}><Icon name="inbox" /></button>
          <button type="button" className="sidebar-header-button" aria-label="Search" aria-current={surface === Surface.Search ? "page" : undefined} onClick={() => navigate(Surface.Search)}><Icon name="search" /></button>
        </div>
      </header>
      <button type="button" className="sidebar-drawer-close" onClick={() => setDrawerOpen(false)}>Close navigation</button>
      {sessionNavigation ? <button type="button" className="sidebar-new-session" aria-current={surface === Surface.NewSession ? "page" : undefined} onClick={(event) => { event.currentTarget.focus(); newSession(); setDrawerOpen(false); }}><Icon name="plus" />New session</button> : null}
      <div ref={list} className="sidebar-list" aria-label={sessionNavigation ? "Project and session navigation" : "Menu navigation and filters"}>
        {sessionNavigation ? <>
        <header className="sidebar-projects-heading"><h2>Projects</h2><button ref={newProjectButton} type="button" className="sidebar-new-project-button" aria-label="New project" onPointerEnter={() => { newProjectPointerInside.current = true; showNewProjectTooltip(); }} onPointerLeave={() => { newProjectPointerInside.current = false; hideNewProjectTooltipWhenInactive(); }} onFocus={() => { newProjectFocused.current = true; showNewProjectTooltip(); }} onBlur={() => { newProjectFocused.current = false; hideNewProjectTooltipWhenInactive(); }} onClick={(event) => { event.currentTarget.focus(); setDrawerOpen(false); openSettings(SettingsEntryDestination.NewProject); }}><Icon name="plus" /></button></header>
        {newProjectTooltip ? createPortal(<div className="sidebar-action-tooltip" role="tooltip" aria-hidden="true" style={{ left: newProjectTooltip.left, top: newProjectTooltip.top }}>New project</div>, window.document.body) : null}
        <label className="sidebar-archived-filter"><input type="checkbox" checked={includeArchived} onChange={(event) => archiveChanged(event.target.checked)} />Include archived</label>
        <QueryProblem error={projects.error} hasData={Boolean(projects.data)} label="projects" retryLabel="Retry project catalog" fetching={projects.isFetching} retry={() => { void projects.refetch(); }} />
        {projects.isPending ? <p className="sidebar-query-state" role="status">Loading projects…</p> : null}
        {!projects.error && projects.data?.resources.length === 0 ? <p className="sidebar-empty">No projects on this page.</p> : null}
        {projectRows.map((project) => <ProjectGroup key={project.id} projectId={project.id} label={projectName(project)} expanded={expandedProjects.has(project.id)} toggle={() => toggleProject(project.id)} page={projectPages.get(project.id) ?? ""} setPage={(page) => setProjectPage(project.id, page)} includeArchived={includeArchived} selected={selectedSessionId} open={chooseSession} active={sessionNavigation} />)}
        {[...fallbackGroups].map(([id, rows]) => {
          return <ProjectGroup key={id} projectId={id} label={`Project · ${id}`} fallback fallbackRows={rows} expanded={!collapsedFallbacks.has(id)} toggle={() => toggleFallback(id)} page="" setPage={() => undefined} includeArchived={includeArchived} selected={selectedSessionId} open={chooseSession} active={sessionNavigation} />;
        })}
        <section className="sidebar-project-group sidebar-general-chat">
          <button type="button" className="sidebar-project-row sidebar-general-chat-heading" aria-expanded={generalExpanded} onClick={() => setGeneralExpanded((current) => !current)}><Icon name="chat" className="sidebar-folder-icon" /><span className="sidebar-project-title">General Chat</span><Icon name="chevron" className={`sidebar-disclosure ${generalExpanded ? "is-expanded" : ""}`} /></button>
          {generalExpanded ? generalRows.map((row) => <SessionRow key={row.id} row={row} selected={selectedSessionId === row.id} open={chooseSession} />) : null}
          {generalExpanded && sessions.data && !sessions.error && generalRows.length === 0 ? <p className="sidebar-empty">No General Chat sessions on this page.</p> : null}
        </section>
        <QueryProblem error={sessions.error} hasData={Boolean(sessions.data)} label="sessions" retryLabel="Retry global sessions" fetching={sessions.isFetching} retry={() => { void sessions.refetch(); }} />
        {sessions.isPending ? <p className="sidebar-query-state" role="status">Loading sessions…</p> : null}
        <nav className="sidebar-global-pager" aria-label="All sessions pages">
          <button type="button" disabled={!globalPage || sessions.isFetching} onClick={() => setGlobalPage("")}>First session page</button>
          <button type="button" disabled={!sessions.data?.nextPageToken || sessions.isFetching} onClick={() => setGlobalPage(sessions.data!.nextPageToken)}>Next session page</button>
        </nav>
        <nav className="sidebar-project-pager" aria-label="Project pages">
          <button type="button" disabled={!projectsPage || projects.isFetching} onClick={() => setProjectsPage("")}>First project page</button>
          <button type="button" disabled={!projects.data?.nextPageToken || projects.isFetching} onClick={() => setProjectsPage(projects.data!.nextPageToken)}>Next project page</button>
        </nav>
        </> : null}
        <div className="sidebar-surface-outlet" ref={setContextTarget} />
      </div>
      <footer className="sidebar-footer">
        <p role="status">{status.error ? "Disconnected · previous data may be stale" : status.data ? "Connected" : status.isPending ? "Connecting…" : "Disconnected"}</p>
      </footer>
    </div>
    </dialog>
  </aside>;
}
