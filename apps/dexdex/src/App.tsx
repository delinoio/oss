/**
 * Root application component for DexDex desktop app.
 * Provides the Linear-style layout with sidebar, tab bar, and content area.
 */

import { type CSSProperties, useCallback, useMemo, useState } from "react";
import "./styles/globals.css";
import { Sidebar } from "./components/sidebar";
import { TabBar } from "./components/tab-bar";
import { CommandPalette } from "./components/command-palette";
import { TaskList } from "./features/tasks/task-list";
import { TaskDetail } from "./features/tasks/task-detail";
import { CreateDialog } from "./features/tasks/create-dialog";
import { InboxPage } from "./features/inbox/inbox-page";
import { PrManagementPage } from "./features/prs/pr-management-page";
import { SettingsPage } from "./features/settings/settings-page";
import { useKeyboardShortcuts } from "./hooks/use-keyboard-shortcuts";
import { useWorkspaceStream } from "./hooks/use-workspace-stream";
import { useTrayStatus } from "./hooks/use-tray-status";
import { useGlobalShortcut } from "./hooks/use-global-shortcut";
import {
  useListUnitTasks,
  useListNotifications,
  useListPullRequests,
  useCreateUnitTaskMutation,
  useSubmitPlanDecisionMutation,
  useMarkNotificationReadMutation,
} from "./hooks/use-dexdex-queries";
import {
  type AppState,
  type AppStore,
  type Theme,
  AppStoreContext,
  getPersistedTheme,
  persistTheme,
  applyThemeToDocument,
} from "./stores/app-store";
import type { Notification, UnitTask } from "./lib/mock-data";
import { PlanDecision } from "./lib/status";
import { PlanDecision as ProtoPlanDecision } from "./gen/v1/dexdex_pb";

const WORKSPACE_ID = "workspace-default";

interface Tab {
  id: string;
  label: string;
  path: string;
}

function App() {
  // App state
  const initialTheme = getPersistedTheme();
  const [theme, setThemeState] = useState<Theme>(initialTheme);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [connectionStatus, setConnectionStatus] = useState<AppState["connectionStatus"]>("connected");
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);

  // Navigation state
  const [currentPath, setCurrentPath] = useState("/tasks");
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [activeTabId, setActiveTabId] = useState("");

  // Data state - Connect RPC queries replace mock data
  const { data: tasks = [] } = useListUnitTasks(WORKSPACE_ID);
  const { data: notifications = [] } = useListNotifications(WORKSPACE_ID);
  const { data: pullRequestsData } = useListPullRequests(WORKSPACE_ID);
  const pullRequests = pullRequestsData?.pullRequests ?? [];
  const markReadMutation = useMarkNotificationReadMutation();

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

  // Build store
  const store: AppStore = useMemo(
    () => ({
      theme,
      sidebarOpen,
      activeWorkspaceId: WORKSPACE_ID,
      connectionStatus,
      toggleTheme,
      setTheme,
      toggleSidebar,
      setSidebarOpen,
      setConnectionStatus,
    }),
    [theme, sidebarOpen, connectionStatus, toggleTheme, setTheme, toggleSidebar],
  );

  // Workspace stream
  useWorkspaceStream({
    workspaceId: WORKSPACE_ID,
    onStatusChange: setConnectionStatus,
  });

  // Tray status sync
  useTrayStatus(WORKSPACE_ID);

  // Navigation
  const navigate = useCallback(
    (path: string) => {
      setCurrentPath(path);

      // If navigating to a task detail, open a tab
      if (path.startsWith("/tasks/")) {
        const taskId = path.replace("/tasks/", "");
        const task = tasks.find((t) => t.unitTaskId === taskId);
        if (task) {
          const existingTab = tabs.find((t) => t.id === taskId);
          if (!existingTab) {
            const newTab: Tab = { id: taskId, label: task.title, path };
            setTabs((prev) => [...prev, newTab]);
          }
          setActiveTabId(taskId);
        }
      }
    },
    [tabs, tasks],
  );

  const handleTabClick = useCallback(
    (tab: Tab) => {
      setActiveTabId(tab.id);
      setCurrentPath(tab.path);
    },
    [],
  );

  const handleTabClose = useCallback(
    (tab: Tab) => {
      setTabs((prev) => prev.filter((t) => t.id !== tab.id));
      if (activeTabId === tab.id) {
        setCurrentPath("/tasks");
        setActiveTabId("");
      }
    },
    [activeTabId],
  );

  // Task actions
  const createTaskMutation = useCreateUnitTaskMutation();

  const handleCreateTask = useCallback(
    (title: string, description: string) => {
      createTaskMutation.mutate({
        workspaceId: WORKSPACE_ID,
        title,
        description,
        repositoryGroupId: "",
      });
    },
    [createTaskMutation],
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
        workspaceId: WORKSPACE_ID,
        subTaskId,
        decision: protoDecision,
        revisionNote: revisionNote ?? "",
      });
    },
    [planDecisionMutation],
  );

  const handleNotificationClick = useCallback(
    (notification: Notification) => {
      if (notification.taskId) {
        navigate(`/tasks/${notification.taskId}`);
      }
    },
    [navigate],
  );

  const handleMarkRead = useCallback(
    (notificationId: string) => {
      markReadMutation.mutate({ workspaceId: WORKSPACE_ID, notificationId });
    },
    [markReadMutation],
  );

  // Global shortcut handler
  useGlobalShortcut({
    workspaceId: WORKSPACE_ID,
    onNavigate: navigate,
  });

  // Keyboard shortcuts
  useKeyboardShortcuts({
    onCommandPalette: () => setCommandPaletteOpen(true),
    onToggleSidebar: toggleSidebar,
    onNavigate: navigate,
    onCreateTask: () => setCreateDialogOpen(true),
  });

  // Render content based on current path
  function renderContent() {
    if (currentPath.startsWith("/tasks/")) {
      const taskId = currentPath.replace("/tasks/", "");
      const task = tasks.find((t) => t.unitTaskId === taskId);
      if (task) {
        return (
          <TaskDetail
            task={task}
            onBack={() => {
              setCurrentPath("/tasks");
              setActiveTabId("");
            }}
            onPlanDecision={handlePlanDecision}
          />
        );
      }
    }

    if (currentPath === "/inbox") {
      return (
        <InboxPage
          notifications={notifications}
          onNotificationClick={handleNotificationClick}
          onMarkRead={handleMarkRead}
        />
      );
    }

    if (currentPath === "/prs") {
      return <PrManagementPage pullRequests={pullRequests} />;
    }

    if (currentPath === "/settings") {
      return <SettingsPage />;
    }

    // Default: task list
    return (
      <TaskList
        tasks={tasks}
        onTaskSelect={(taskId) => navigate(`/tasks/${taskId}`)}
        onCreateTask={() => setCreateDialogOpen(true)}
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
          <div style={contentAreaStyle}>{renderContent()}</div>
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
        onClose={() => setCreateDialogOpen(false)}
        onCreate={handleCreateTask}
      />
    </AppStoreContext.Provider>
  );
}

export default App;
