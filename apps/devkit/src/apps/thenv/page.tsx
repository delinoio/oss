import { activateVersionAction, upsertPolicyBindingAction } from "./actions";
import { loadDashboard, type ThenvScope } from "@/server/thenv-api";

export const dynamic = "force-dynamic";

function resolveScope(searchParams: Record<string, string | string[] | undefined>): ThenvScope {
  const read = (key: string, fallback: string): string => {
    const value = searchParams[key];
    if (typeof value === "string") {
      return value.trim() || fallback;
    }
    return fallback;
  };

  return {
    workspaceId: read("workspace", "default-workspace"),
    projectId: read("project", "default-project"),
    environmentId: read("env", "dev"),
  };
}

export default async function ThenvMiniAppPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const resolvedSearchParams = await searchParams;
  const scope = resolveScope(resolvedSearchParams);
  const dashboard = await loadDashboard(scope);

  return (
    <main>
      <section className="panel grid">
        <div className="header">
          <div>
            <h1>thenv Console</h1>
            <p>Metadata-only management for bundle versions, policy bindings, and audit events.</p>
          </div>
          <span className="badge">Scope: {scope.workspaceId}/{scope.projectId}/{scope.environmentId}</span>
        </div>

        <form method="get" className="controls">
          <label>
            Workspace
            <input type="text" name="workspace" defaultValue={scope.workspaceId} />
          </label>
          <label>
            Project
            <input type="text" name="project" defaultValue={scope.projectId} />
          </label>
          <label>
            Environment
            <input type="text" name="env" defaultValue={scope.environmentId} />
          </label>
          <button type="submit">Load Scope</button>
        </form>

        {dashboard.failures.length > 0 ? (
          <div className="alert">
            <strong>Some sections failed to load:</strong>
            <ul>
              {dashboard.failures.map((failure) => (
                <li key={failure}>{failure}</li>
              ))}
            </ul>
          </div>
        ) : null}
      </section>

      <section className="panel" style={{ marginTop: "1rem" }}>
        <h2>Bundle Versions</h2>
        <table>
          <thead>
            <tr>
              <th>Version ID</th>
              <th>Status</th>
              <th>Created By</th>
              <th>Created At</th>
              <th>Action</th>
            </tr>
          </thead>
          <tbody>
            {dashboard.versions.length === 0 ? (
              <tr>
                <td colSpan={5}>No versions available in this scope.</td>
              </tr>
            ) : (
              dashboard.versions.map((version) => (
                <tr key={version.bundleVersionId}>
                  <td>{version.bundleVersionId}</td>
                  <td>{version.status}</td>
                  <td>{version.createdBy || "-"}</td>
                  <td>{version.createdAt || "-"}</td>
                  <td>
                    <form action={activateVersionAction}>
                      <input type="hidden" name="workspace" value={scope.workspaceId} />
                      <input type="hidden" name="project" value={scope.projectId} />
                      <input type="hidden" name="env" value={scope.environmentId} />
                      <input type="hidden" name="bundleVersionId" value={version.bundleVersionId} />
                      <button type="submit">Activate</button>
                    </form>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </section>

      <section className="grid grid-2" style={{ marginTop: "1rem" }}>
        <article className="panel">
          <h2>Policy Bindings</h2>
          <p>Revision: {dashboard.policyRevision}</p>
          <table>
            <thead>
              <tr>
                <th>Subject</th>
                <th>Role</th>
              </tr>
            </thead>
            <tbody>
              {dashboard.policy.length === 0 ? (
                <tr>
                  <td colSpan={2}>No policy bindings configured.</td>
                </tr>
              ) : (
                dashboard.policy.map((binding) => (
                  <tr key={binding.subject}>
                    <td>{binding.subject}</td>
                    <td>{binding.role}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>

          <h3 style={{ marginTop: "1rem" }}>Add / Update Binding</h3>
          <form action={upsertPolicyBindingAction} className="controls">
            <input type="hidden" name="workspace" value={scope.workspaceId} />
            <input type="hidden" name="project" value={scope.projectId} />
            <input type="hidden" name="env" value={scope.environmentId} />
            <label>
              Subject
              <input type="text" name="subject" required />
            </label>
            <label>
              Role
              <select name="role" defaultValue="reader">
                <option value="reader">reader</option>
                <option value="writer">writer</option>
                <option value="admin">admin</option>
              </select>
            </label>
            <button type="submit">Save Binding</button>
          </form>
        </article>

        <article className="panel">
          <h2>Audit Events</h2>
          <table>
            <thead>
              <tr>
                <th>Type</th>
                <th>Actor</th>
                <th>Result</th>
                <th>Created At</th>
              </tr>
            </thead>
            <tbody>
              {dashboard.auditEvents.length === 0 ? (
                <tr>
                  <td colSpan={4}>No audit events available.</td>
                </tr>
              ) : (
                dashboard.auditEvents.map((event) => (
                  <tr key={event.eventId}>
                    <td>{event.eventType}</td>
                    <td>{event.actor || "-"}</td>
                    <td>{event.result || "-"}</td>
                    <td>{event.createdAt || "-"}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </article>
      </section>
    </main>
  );
}
