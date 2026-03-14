import { useNavigate } from "react-router";
import { X } from "lucide-react";
import { cn } from "../lib/cn";
import { useAppStore } from "../stores/app-store";

export function TabBar() {
  const navigate = useNavigate();
  const { openTabs, activeTabId, setActiveTab, closeTab } = useAppStore();

  if (openTabs.length === 0) {
    return null;
  }

  function handleTabClick(id: string, path: string) {
    setActiveTab(id);
    navigate(path);
  }

  function handleClose(e: React.MouseEvent, id: string) {
    e.stopPropagation();
    closeTab(id);
  }

  function handleAuxClick(e: React.MouseEvent, id: string) {
    // Middle mouse button
    if (e.button === 1) {
      e.preventDefault();
      closeTab(id);
    }
  }

  return (
    <div className="flex items-center border-b border-[var(--color-border-default)] bg-[var(--color-bg-primary)] h-9 overflow-x-auto">
      {openTabs.map((tab) => {
        const isActive = tab.id === activeTabId;
        return (
          <button
            key={tab.id}
            onClick={() => handleTabClick(tab.id, tab.path)}
            onAuxClick={(e) => handleAuxClick(e, tab.id)}
            className={cn(
              "flex items-center gap-1.5 px-3 h-full text-[12px] border-r border-[var(--color-border-default)] whitespace-nowrap transition-colors group",
              isActive
                ? "text-[var(--color-text-primary)] border-b-2 border-b-[var(--color-border-accent)]"
                : "text-[var(--color-text-secondary)] hover:text-[var(--color-text-primary)] hover:bg-[var(--color-bg-hover)]",
            )}
            type="button"
          >
            <span className="truncate max-w-[160px]">{tab.title}</span>
            <span
              role="button"
              tabIndex={-1}
              onClick={(e) => handleClose(e, tab.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter") handleClose(e as unknown as React.MouseEvent, tab.id);
              }}
              className="flex items-center justify-center w-4 h-4 rounded opacity-0 group-hover:opacity-100 hover:bg-[var(--color-bg-hover)] transition-opacity"
            >
              <X size={12} />
            </span>
          </button>
        );
      })}
    </div>
  );
}
