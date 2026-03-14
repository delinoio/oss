import { Monitor, Moon, Sun } from "lucide-react";
import { cn } from "../../lib/cn";
import { useAppStore } from "../../stores/app-store";
import { mockWorkspace } from "../../lib/mock-data";

export function SettingsPage() {
  const { theme, toggleTheme } = useAppStore();

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="px-6 py-3 border-b border-[var(--color-border-default)]">
        <h1 className="text-[15px] font-semibold text-[var(--color-text-primary)]">
          Settings
        </h1>
      </div>

      {/* Settings content */}
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {/* Theme */}
        <section>
          <h2 className="text-[13px] font-semibold text-[var(--color-text-primary)] mb-3">
            Appearance
          </h2>
          <div className="flex gap-2">
            <button
              onClick={() => {
                if (theme !== "light") toggleTheme();
              }}
              className={cn(
                "flex items-center gap-2 px-4 py-2.5 text-[13px] font-medium border rounded transition-colors",
                theme === "light"
                  ? "border-[var(--color-border-accent)] bg-[var(--color-bg-accent)]/5 text-[var(--color-text-accent)]"
                  : "border-[var(--color-border-default)] text-[var(--color-text-secondary)] hover:border-[var(--color-border-accent)] hover:text-[var(--color-text-primary)]",
              )}
              type="button"
            >
              <Sun size={16} />
              Light
            </button>
            <button
              onClick={() => {
                if (theme !== "dark") toggleTheme();
              }}
              className={cn(
                "flex items-center gap-2 px-4 py-2.5 text-[13px] font-medium border rounded transition-colors",
                theme === "dark"
                  ? "border-[var(--color-border-accent)] bg-[var(--color-bg-accent)]/5 text-[var(--color-text-accent)]"
                  : "border-[var(--color-border-default)] text-[var(--color-text-secondary)] hover:border-[var(--color-border-accent)] hover:text-[var(--color-text-primary)]",
              )}
              type="button"
            >
              <Moon size={16} />
              Dark
            </button>
          </div>
        </section>

        {/* Workspace */}
        <section>
          <h2 className="text-[13px] font-semibold text-[var(--color-text-primary)] mb-3">
            Workspace
          </h2>
          <div className="space-y-2">
            <div className="flex items-center gap-3">
              <span className="text-[12px] text-[var(--color-text-secondary)] w-20">
                Name
              </span>
              <span className="text-[13px] text-[var(--color-text-primary)]">
                {mockWorkspace.name}
              </span>
            </div>
            <div className="flex items-center gap-3">
              <span className="text-[12px] text-[var(--color-text-secondary)] w-20">
                Endpoint
              </span>
              <code className="text-[12px] text-[var(--color-text-primary)] bg-[var(--color-bg-secondary)] px-2 py-0.5 rounded">
                {mockWorkspace.endpoint}
              </code>
            </div>
            <div className="flex items-center gap-3">
              <span className="text-[12px] text-[var(--color-text-secondary)] w-20">
                ID
              </span>
              <code className="text-[12px] text-[var(--color-text-tertiary)] bg-[var(--color-bg-secondary)] px-2 py-0.5 rounded">
                {mockWorkspace.workspaceId}
              </code>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
