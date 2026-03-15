/**
 * Root application component for DexDex desktop app.
 * Provides the Linear-style layout with sidebar, tab bar, and content area.
 * Uses react-router for page navigation.
 */

import { type CSSProperties, useCallback, useEffect, useMemo, useState } from "react";
import { Routes, Route, useNavigate, useLocation, Navigate } from "react-router";
import "./styles/globals.css";
import { Sidebar } from "./components/sidebar";
import { TabBar } from "./components/tab-bar";
import { CommandPalette } from "./components/command-palette";
import { ErrorBoundary } from "./components/error-boundary";
import { TaskList } from "./features/tasks/task-list";
import { TaskDetail } from "./features/tasks/task-detail";
import { CreateDialog } from "./features/tasks/create-dialog";
import { InboxPage } from "./features/inbox/inbox-page";
import { PrManagementPage } from "./features/prs/pr-management-page";
import { PrDetailPage } from "./features/prs/pr-detail-page";
import { SettingsPage } from "./features/settings/settings-page";
import { useKeyboardShortcuts } from "./hooks/use-keyboard-shortcuts";
import { useWorkspaceStream } from "./hooks/use-workspace-stream";
import { useTrayStatus } from "./hooks/use-tray-status";
import { useGlobalShortcut } from "./hooks/use-global-shortcut";
import { useWebNotifications } from "./hooks/use-web-notifications";
import { useQueryClient } from "@tanstack/react-query";
import {
  useListUnitTasks,
  useListPullRequests,
  useCreateUnitTaskMutation,
  useSubmitPlanDecisionMutation,
} from "./hooks/use-dexdex-queries";
import {
  type AppState,
  type AppStore,
  type Theme,
  AppStoreContext,
  getPersistedTheme,
  persistTheme,
  applyThemeToDocument,
  getPersistedActiveWorkspaceId,
  persistActiveWorkspaceId,
} from "./stores/app-store";
import { summarizePrompt } from "./lib/adapters";
import { PlanDecision } from "./lib/status";
import { AgentCliType, PlanDecision as ProtoPlanDecision } from "./gen/v1/dexdex_pb";

interface Tab {
  id: string;
  label: string;
  path: string;
}

function App() {
  const routerNavigate = useNavigate();
  const location = useLocation();
  const currentPath = location.pathname;

  // App state
  const initialTheme = getPersistedTheme();
  const [theme, setThemeState] = useState<Theme>(initialTheme);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [activeWorkspaceId, setActiveWorkspaceIdState] = useState(() => getPersistedActiveWorkspaceId());
  const [connectionStatus, setConnectionStatus] = useState<AppState["connectionStatus"]>("connected");
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [selectedTaskIndex, setSelectedTaskIndex] = useState(-1);

  // Tab state
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [activeTabId, setActiveTabId] = useState("");

  // Data state - Connect RPC queries replace mock data
  const { data: tasks = [], isLoading: tasksLoading } = useListUnitTasks(activeWorkspaceId);
  const { data: pullRequestsData, isLoading: prsLoading } = useListPullRequests(activeWorkspaceId);
  const pullRequests = pullRequestsData?.pullRequests ?? [];

  // Apply initial theme
  useMemo(() => {
    applyThemeToDocument(initialTheme);
  }, [initialTheme]);

  const setTheme = useCallback((newTheme: Theme) => {
    setThemeState(newTheme);
    persistTheme(newTheme);
    applyThemeToDocument(newTheme);
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme(theme === "light" ? "dark" : "light");
  }, [theme, setTheme]);

  const toggleSidebar = useCallback(() => {
    setSidebarOpen((prev) => !prev);
  }, []);

  const setActiveWorkspaceId = useCallback((id: string) => {
    setActiveWorkspaceIdState(id);
    persistActiveWorkspaceId(id);
  }, []);

  // Build store
  const store: AppStore = useMemo(
    () => ({
      theme,
      sidebarOpen,
      activeWorkspaceId,
      connectionStatus,
      toggleTheme,
      setTheme,
      toggleSidebar,
      setSidebarOpen,
      setActiveWorkspaceId,
      setConnectionStatus,
    }),
    [theme, sidebarOpen, activeWorkspaceId, connectionStatus, toggleTheme, setTheme, toggleSidebar, setActiveWorkspaceId],
  );

  // Invalidate all queries when workspace changes
  const queryClient = useQueryClient();
  useEffect(() => {
    queryClient.invalidateQueries();
  }, [activeWorkspaceId, queryClient]);

  // Web Notifications
  const { dispatchNotification } = useWebNotifications({ onNavigate: routerNavigate });

  // Workspace stream
  useWorkspaceStream({
    workspaceId: activeWorkspaceId,
    onStatusChange: setConnectionStatus,
    onNotification: dispatchNotification,
  });

  // Tray status sync
  useTrayStatus(activeWorkspaceId);

  // Navigation - wraps react-router navigate with tab management
  const navigate = useCallback(
    (path: string) => {
      routerNavigate(path);

      // If navigating to a task detail, open a tab
      if (path.startsWith("/tasks/")) {
        const taskId = path.replace("/tasks/", "");
        const task = tasks.find((t) => t.unitTaskId === taskId);
        if (task) {
          const existingTab = tabs.find((t) => t.id === taskId);
          if (!existingTab) {
            const newTab: Tab = { id: taskId, label: summarizePrompt(task.prompt ?? task.title), path };
            setTabs((prev) => [...prev, newTab]);
          }
          setActiveTabId(taskId);
        }
      }
    },
    [routerNavigate, tabs, tasks],
  );

  const handleTabClick = useCallback(
    (tab: Tab) => {
      setActiveTabId(tab.id);
      routerNavigate(tab.path);
    },
    [routerNavigate],
  );

  const handleTabClose = useCallback(
    (tab: Tab) => {
      setTabs((prev) => prev.filter((t) => t.id !== tab.id));
      if (activeTabId === tab.id) {
        routerNavigate("/tasks");
        setActiveTabId("");
      }
    },
    [activeTabId, routerNavigate],
  );

  // Task actions
  const createTaskMutation = useCreateUnitTaskMutation();

  const handleCreateTask = useCallback(
    (prompt: string, repositoryGroupId: string, agentCliType: AgentCliType, usePlanMode: boolean) => {
      createTaskMutation.mutate({
        workspaceId: activeWorkspaceId,
        prompt,
        repositoryGroupId,
        agentCliType,
        usePlanMode,
      });
    },
    [createTaskMutation, activeWorkspaceId],
  );

  const planDecisionMutation = useSubmitPlanDecisionMutation();

  const handlePlanDecision = useCallback(
    (subTaskId: string, decision: PlanDecision, revisionNote?: string) => {
      const protoDecision =
        decision === PlanDecision.APPROVE
          ? ProtoPlanDecision.APPROVE
          : decision === PlanDecision.REVISE
            ? ProtoPlanDecision.REVISE
            : ProtoPlanDecision.REJECT;
      planDecisionMutation.mutate({
        workspaceId: activeWorkspaceId,
        subTaskId,
        decision: protoDecision,
        revisionNote: revisionNote ?? "",
      });
    },
    [planDecisionMutation, activeWorkspaceId],
  );

  // Global shortcut handler
  useGlobalShortcut({
    workspaceId: activeWorkspaceId,
    onNavigate: navigate,
  });

  // Reset selectedTaskIndex when navigating away from tasks
  useEffect(() => {
    if (currentPath !== "/tasks") {
      setSelectedTaskIndex(-1);
    }
  }, [currentPath]);

  // Keyboard shortcuts
  useKeyboardShortcuts({
    onCommandPalette: () => setCommandPaletteOpen(true),
    onToggleSidebar: toggleSidebar,
    onNavigate: navigate,
    onCreateTask: () => setCreateDialogOpen(true),
    onCloseTab: () => {
      const activeTab = tabs.find((t) => t.id === activeTabId);
      if (activeTab) {
        handleTabClose(activeTab);
      }
    },
    onListDown: () => {
      if (currentPath === "/tasks") {
        setSelectedTaskIndex((prev) => Math.min(prev + 1, tasks.length - 1));
      }
    },
    onListUp: () => {
      if (currentPath === "/tasks") {
        setSelectedTaskIndex((prev) => Math.max(prev - 1, 0));
      }
    },
    onSwitchTabLeft: () => {
      if (tabs.length === 0) return;
      const currentIdx = tabs.findIndex((t) => t.id === activeTabId);
      if (currentIdx > 0) {
        const prevTab = tabs[currentIdx - 1];
        handleTabClick(prevTab);
      }
    },
    onSwitchTabRight: () => {
      if (tabs.length === 0) return;
      const currentIdx = tabs.findIndex((t) => t.id === activeTabId);
      if (currentIdx < tabs.length - 1) {
        const nextTab = tabs[currentIdx + 1];
        handleTabClick(nextTab);
      }
    },
    onApprovePlan: () => {
      // Context-sensitive: only if on task detail with waiting subtask
      if (!currentPath.startsWith("/tasks/")) return;
      const taskId = currentPath.replace("/tasks/", "");
      const task = tasks.find((t) => t.unitTaskId === taskId);
      if (task) {
        // We need to find a waiting subtask - delegate to handlePlanDecision
        // This is a simplified version - the full version would need subtask data
        handlePlanDecision(taskId, PlanDecision.APPROVE);
      }
    },
    onRevisePlan: () => {
      // For V key - context sensitive
      if (!currentPath.startsWith("/tasks/")) return;
    },
    onRejectPlan: () => {
      // For Shift+X - context sensitive: cancel task if in progress, reject plan if waiting
      if (!currentPath.startsWith("/tasks/")) return;
    },
  });

  // Task detail renderer (used by route)
  function TaskDetailRoute() {
    const taskId = currentPath.replace("/tasks/", "");
    const task = tasks.find((t) => t.unitTaskId === taskId);
    if (!task) {
      return <Navigate to="/tasks" replace />;
    }
    return (
      <TaskDetail
        task={task}
        onBack={() => {
          routerNavigate("/tasks");
          setActiveTabId("");
        }}
        onPlanDecision={handlePlanDecision}
      />
    );
  }

  // PR detail renderer (used by route)
  function PrDetailRoute() {
    const prTrackingId = currentPath.replace("/prs/", "");
    if (!prTrackingId) {
      return <Navigate to="/prs" replace />;
    }
    return (
      <PrDetailPage
        workspaceId={activeWorkspaceId}
        prTrackingId={prTrackingId}
        onBack={() => routerNavigate("/prs")}
      />
    );
  }

  const layoutStyle: CSSProperties = {
    display: "flex",
    height: "100%",
    width: "100%",
    overflow: "hidden",
  };

  const mainStyle: CSSProperties = {
    flex: 1,
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
    minWidth: 0,
  };

  const contentAreaStyle: CSSProperties = {
    flex: 1,
    overflow: "hidden",
  };

  return (
    <AppStoreContext.Provider value={store}>
      <div style={layoutStyle} data-testid="app-layout">
        <Sidebar activePath={currentPath} onNavigate={navigate} />
        <div style={mainStyle}>
          <TabBar
            tabs={tabs}
            activeTabId={activeTabId}
            onTabClick={handleTabClick}
            onTabClose={handleTabClose}
          />
          <div style={contentAreaStyle}>
            <ErrorBoundary>
              <Routes>
                <Route
                  path="/tasks"
                  element={
                    <TaskList
                      tasks={tasks}
                      isLoading={tasksLoading}
                      onTaskSelect={(taskId) => navigate(`/tasks/${taskId}`)}
                      onCreateTask={() => setCreateDialogOpen(true)}
                      selectedIndex={selectedTaskIndex}
                      onSelectIndex={setSelectedTaskIndex}
                    />
                  }
                />
                <Route path="/tasks/:taskId" element={<TaskDetailRoute />} />
                <Route
                  path="/inbox"
                  element={<InboxPage />}
                />
                <Route
                  path="/prs"
                  element={
                    <PrManagementPage
                      pullRequests={pullRequests}
                      isLoading={prsLoading}
                      workspaceId={activeWorkspaceId}
                      onPrSelect={(prTrackingId) => navigate(`/prs/${prTrackingId}`)}
                    />
                  }
                />
                <Route
                  path="/prs/:prTrackingId"
                  element={<PrDetailRoute />}
                />
                <Route path="/settings" element={<SettingsPage />} />
                <Route path="*" element={<Navigate to="/tasks" replace />} />
              </Routes>
            </ErrorBoundary>
          </div>
        </div>
      </div>
      <CommandPalette
        isOpen={commandPaletteOpen}
        onClose={() => setCommandPaletteOpen(false)}
        onNavigate={navigate}
        onCreateTask={() => {
          setCreateDialogOpen(true);
          setCommandPaletteOpen(false);
        }}
        tasks={tasks}
      />
      <CreateDialog
        isOpen={createDialogOpen}
        workspaceId={activeWorkspaceId}
        onClose={() => setCreateDialogOpen(false)}
        onCreate={handleCreateTask}
      />
    </AppStoreContext.Provider>
  );
}

export default App;
