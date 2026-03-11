import { useQuery } from "@connectrpc/connect-query";
import { listRepositoryGroups } from "../../gen/v1/dexdex-RepositoryService_connectquery";
import { listUnitTasks } from "../../gen/v1/dexdex-TaskService_connectquery";
import { getWorkspaceOverview } from "../../gen/v1/dexdex-WorkspaceService_connectquery";
import {
  UnitTaskStatus,
  type RepositoryGroup,
  type WorkspaceOverview,
} from "../../gen/v1/dexdex_pb";
import {
  visualRepositoryGroups,
  visualUnitTasks,
  visualWorkspaceOverview,
} from "../../lib/visual-fixtures";
import { unitTaskDotClass } from "../../components/ui/StatusDot";

const defaultListPageSize = 50;

function enumLabel<T extends Record<string, string | number>>(enumType: T, value: number): string {
  const maybeLabel = enumType[value as unknown as keyof T];
  return typeof maybeLabel === "string" ? maybeLabel : "UNSPECIFIED";
}

type ProjectsPageProps = {
  workspaceId: string;
  visualMode: boolean;
};

export function ProjectsPage({ workspaceId, visualMode }: ProjectsPageProps) {
  const overviewQuery = useQuery(getWorkspaceOverview, { workspaceId }, { enabled: !visualMode });
  const repositoryGroupsQuery = useQuery(
    listRepositoryGroups,
    { workspaceId, pageSize: defaultListPageSize, pageToken: "" },
    { enabled: !visualMode },
  );
  const unitTasksQuery = useQuery(
    listUnitTasks,
    { workspaceId, status: UnitTaskStatus.UNSPECIFIED, pageSize: defaultListPageSize, pageToken: "" },
    { enabled: !visualMode },
  );

  const overview: WorkspaceOverview | undefined = visualMode
    ? visualWorkspaceOverview
    : overviewQuery.data?.overview;
  const repositoryGroups: RepositoryGroup[] = visualMode
    ? visualRepositoryGroups
    : (repositoryGroupsQuery.data?.items ?? []);
  const activeTasks = (visualMode ? visualUnitTasks : unitTasksQuery.data?.items ?? []).filter(
    (task) =>
      task.status === UnitTaskStatus.IN_PROGRESS ||
      task.status === UnitTaskStatus.ACTION_REQUIRED ||
      task.status === UnitTaskStatus.BLOCKED,
  );

  return (
    <div className="content-body">
      <div className="dashboard-grid">
        <section className="panel">
          <header className="panel-header">Workspace Overview</header>
          <div className="panel-body">
            {overview ? (
              <div className="metric-grid">
                <div className="metric-card">
                  <span className="metric-label">Total Unit Tasks</span>
                  <span className="metric-value">{overview.totalUnitTaskCount}</span>
                </div>
                <div className="metric-card">
                  <span className="metric-label">Action Required</span>
                  <span className="metric-value">{overview.actionRequiredUnitTaskCount}</span>
                </div>
                <div className="metric-card">
                  <span className="metric-label">Active Sessions</span>
                  <span className="metric-value">{overview.activeSessionCount}</span>
                </div>
                <div className="metric-card">
                  <span className="metric-label">Open PRs</span>
                  <span className="metric-value">{overview.openPullRequestCount}</span>
                </div>
              </div>
            ) : overviewQuery.isPending ? (
              <p className="text-muted text-sm">Loading workspace overview...</p>
            ) : (
              <p className="empty-state">No overview data available.</p>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Repository Groups</header>
          <div className="panel-body">
            {repositoryGroups.length > 0 ? (
              <ul className="item-list">
                {repositoryGroups.map((group) => (
                  <li key={group.repositoryGroupId} className="panel-list-item">
                    <p className="item-row-title">{group.repositoryGroupId}</p>
                    <p className="item-row-sub">{group.repositories.length} repositories</p>
                  </li>
                ))}
              </ul>
            ) : repositoryGroupsQuery.isPending ? (
              <p className="text-muted text-sm">Loading repository groups...</p>
            ) : (
              <p className="empty-state">No repository groups found.</p>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Active Task Summary</header>
          <div className="panel-body">
            {activeTasks.length > 0 ? (
              <ul className="item-list">
                {activeTasks.map((task) => (
                  <li key={task.unitTaskId} className="panel-list-item">
                    <div className="inline-gap">
                      <span className={`item-row-dot ${unitTaskDotClass(task.status)}`} />
                      <span className="item-row-title">{task.unitTaskId}</span>
                    </div>
                    <p className="item-row-sub">{enumLabel(UnitTaskStatus, task.status)}</p>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="empty-state">No active tasks.</p>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
