import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import type { DexDexPageDefinition } from "../contracts/dexdex-page";

export type SidebarStat = {
  label: string;
  value: string | number;
};

type DesktopShellFrameProps = {
  workspaceId: string;
  status: "idle" | "resolving" | "resolved" | "error";
  pages: ReadonlyArray<DexDexPageDefinition>;
  activePage: DexDexPageDefinition | null;
  onSwitchWorkspace: () => void;
  sidebarStats: SidebarStat[];
  sidebarBody: ReactNode;
  connectionMode: string;
  mainContent: ReactNode;
  rightPanel: ReactNode;
};

export function DesktopShellFrame({
  workspaceId,
  status,
  pages,
  activePage,
  onSwitchWorkspace,
  sidebarStats,
  sidebarBody,
  connectionMode,
  mainContent,
  rightPanel,
}: DesktopShellFrameProps) {
  return (
    <div className="desktop-shell">
      <header className="topbar">
        <div className="topbar-left">
          <span className="topbar-logo">DexDex</span>
          <span className="topbar-separator" />
          <span className="topbar-workspace">{workspaceId}</span>
          <span className={`topbar-status topbar-status-${status}`} />
        </div>
        <div className="topbar-right">
          <button type="button" className="btn btn-ghost btn-sm" onClick={onSwitchWorkspace}>
            Switch
          </button>
        </div>
      </header>

      <div className="desktop-body">
        <aside className="sidebar" aria-label="Desktop navigation">
          <nav className="sidebar-nav">
            {pages.map((page) => (
              <NavLink
                key={page.id}
                to={page.path}
                className={({ isActive }) =>
                  `sidebar-nav-item ${isActive ? "sidebar-nav-item-active" : ""}`
                }
              >
                {page.label}
              </NavLink>
            ))}
          </nav>

          <div className="sidebar-stats">
            {sidebarStats.map((stat) => (
              <div key={stat.label} className="sidebar-stat">
                <span className="sidebar-stat-label">{stat.label}</span>
                <span className="sidebar-stat-value">{stat.value}</span>
              </div>
            ))}
          </div>

          <div className="sidebar-body">{sidebarBody}</div>

          <footer className="sidebar-footer">
            <div className="sidebar-connection">
              <span>Connection</span>
              <strong>{connectionMode}</strong>
            </div>
          </footer>
        </aside>

        <main className="main-content">
          {activePage ? (
            <header className="content-header">
              <h2>{activePage.label}</h2>
            </header>
          ) : null}
          {mainContent}
        </main>

        {rightPanel}
      </div>
    </div>
  );
}
