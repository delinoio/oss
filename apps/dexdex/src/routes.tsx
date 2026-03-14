import { Navigate, Route, Routes } from "react-router";
import { TaskListPage } from "./features/tasks/task-list-page";
import { TaskDetailPage } from "./features/tasks/task-detail-page";
import { InboxPage } from "./features/inbox/inbox-page";
import { SettingsPage } from "./features/settings/settings-page";

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/tasks" replace />} />
      <Route path="/tasks" element={<TaskListPage />} />
      <Route path="/tasks/:taskId" element={<TaskDetailPage />} />
      <Route path="/inbox" element={<InboxPage />} />
      <Route path="/settings" element={<SettingsPage />} />
    </Routes>
  );
}
