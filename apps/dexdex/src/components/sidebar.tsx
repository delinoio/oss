import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router";
import {
  CheckSquare,
  Inbox,
  Settings,
  ChevronDown,
  PanelLeftClose,
  PanelLeft,
} from "lucide-react";
import { cn } from "../lib/cn";
import { useAppStore } from "../stores/app-store";

interface NavItem {
  id: string;
  label: string;
  icon: React.ComponentType<{ size?: number; className?: string }>;
  path: string;
}

const navItems: NavItem[] = [
  { id: "tasks", label: "Tasks", icon: CheckSquare, path: "/tasks" },
  { id: "inbox", label: "Inbox", icon: Inbox, path: "/inbox" },
  { id: "settings", label: "Settings", icon: Settings, path: "/settings" },
];

export function Sidebar() {
  const location = useLocation();
  const navigate = useNavigate();
  const { sidebarCollapsed, toggleSidebar } = useAppStore();

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "b") {
        e.preventDefault();
        toggleSidebar();
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [toggleSidebar]);

  return (
    <aside
      className={cn(
        "flex flex-col border-r border-[var(--color-border-default)] bg-[var(--color-bg-sidebar)] transition-all duration-200",
        sidebarCollapsed ? "w-12" : "w-60",
      )}
    >
      {/* Workspace header */}
      <div
        className={cn(
          "flex items-center border-b border-[var(--color-border-default)] px-3 h-12",
          sidebarCollapsed ? "justify-center" : "justify-between",
        )}
      >
        {!sidebarCollapsed && (
          <button
            className="flex items-center gap-1.5 text-[13px] font-semibold text-[var(--color-text-primary)] hover:text-[var(--color-text-accent)] transition-colors"
            type="button"
          >
            <span className="truncate">DexDex</span>
            <ChevronDown size={14} className="text-[var(--color-text-tertiary)]" />
          </button>
        )}
        <button
          onClick={toggleSidebar}
          className="flex items-center justify-center w-7 h-7 rounded text-[var(--color-text-tertiary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-hover)] transition-colors"
          title={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
          type="button"
        >
          {sidebarCollapsed ? <PanelLeft size={16} /> : <PanelLeftClose size={16} />}
        </button>
      </div>

      {/* Navigation */}
      <nav className="flex-1 py-2 px-1.5">
        {navItems.map((item) => {
          const isActive = location.pathname.startsWith(item.path);
          const Icon = item.icon;
          return (
            <button
              key={item.id}
              onClick={() => navigate(item.path)}
              className={cn(
                "flex items-center w-full gap-2.5 px-2.5 py-1.5 text-[13px] rounded transition-colors",
                isActive
                  ? "bg-[var(--color-bg-active)] text-[var(--color-text-accent)] font-medium border-l-2 border-[var(--color-border-accent)]"
                  : "text-[var(--color-text-secondary)] hover:bg-[var(--color-bg-hover)] hover:text-[var(--color-text-primary)]",
                sidebarCollapsed && "justify-center px-0",
              )}
              title={sidebarCollapsed ? item.label : undefined}
              type="button"
            >
              <Icon size={16} />
              {!sidebarCollapsed && <span>{item.label}</span>}
            </button>
          );
        })}
      </nav>
    </aside>
  );
}
