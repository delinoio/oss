"use client";

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";

import {
  activateVersion,
  getPolicy,
  listAuditEvents,
  listVersions,
  setPolicy,
} from "@/apps/thenv/api-client";
import {
  DEFAULT_THENV_SCOPE,
  ThenvAuditEvent,
  ThenvBundleStatus,
  ThenvBundleVersionSummary,
  ThenvPolicyBinding,
  ThenvRole,
  ThenvScope,
} from "@/apps/thenv/contracts";
import { LogEvent, logError, logInfo } from "@/lib/logger";

const ROLE_OPTIONS: readonly ThenvRole[] = [
  ThenvRole.Reader,
  ThenvRole.Writer,
  ThenvRole.Admin,
];

function formatTimestamp(value?: string): string {
  if (!value) {
    return "-";
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }
  return parsed.toISOString();
}

function roleLabel(role: ThenvRole): string {
  switch (role) {
    case ThenvRole.Reader:
      return "Reader";
    case ThenvRole.Writer:
      return "Writer";
    case ThenvRole.Admin:
      return "Admin";
    default:
      return role;
  }
}

export function ThenvConsole() {
  const [scope, setScope] = useState<ThenvScope>(DEFAULT_THENV_SCOPE);
  const [versions, setVersions] = useState<ThenvBundleVersionSummary[]>([]);
  const [auditEvents, setAuditEvents] = useState<ThenvAuditEvent[]>([]);
  const [bindings, setBindings] = useState<ThenvPolicyBinding[]>([]);
  const [policyRevision, setPolicyRevision] = useState<number>(0);

  const [activateTarget, setActivateTarget] = useState<string>("");
  const [newSubject, setNewSubject] = useState<string>("");
  const [newRole, setNewRole] = useState<ThenvRole>(ThenvRole.Reader);
  const [draftBindings, setDraftBindings] = useState<ThenvPolicyBinding[]>([]);

  const [loading, setLoading] = useState<boolean>(false);
  const [savingPolicy, setSavingPolicy] = useState<boolean>(false);
  const [activating, setActivating] = useState<boolean>(false);
  const [errorMessage, setErrorMessage] = useState<string>("");

  const activeVersion = useMemo(
    () =>
      versions.find(
        (version) => version.status === ThenvBundleStatus.Active,
      )?.bundleVersionId ?? "",
    [versions],
  );

  const loadConsoleData = useCallback(async () => {
    setLoading(true);
    setErrorMessage("");

    try {
      const [versionsResponse, policyResponse, auditResponse] = await Promise.all([
        listVersions(scope),
        getPolicy(scope),
        listAuditEvents(scope),
      ]);

      setVersions(versionsResponse.versions);
      setBindings(policyResponse.bindings);
      setPolicyRevision(policyResponse.policyRevision);
      setDraftBindings(policyResponse.bindings);
      setAuditEvents(auditResponse.events);

      logInfo({
        event: LogEvent.RouteRender,
        route: "/apps/thenv",
        message: "Loaded thenv metadata console state.",
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Failed to load thenv data.";
      setErrorMessage(message);
      logError({
        event: LogEvent.RouteLoadError,
        route: "/apps/thenv",
        message,
        error,
      });
    } finally {
      setLoading(false);
    }
  }, [scope]);

  useEffect(() => {
    void loadConsoleData();
  }, [loadConsoleData]);

  const handleScopeChange = (key: keyof ThenvScope, value: string) => {
    setScope((previous) => ({ ...previous, [key]: value }));
  };

  const handleRefresh = (event: FormEvent) => {
    event.preventDefault();
    void loadConsoleData();
  };

  const handleActivate = async (event: FormEvent) => {
    event.preventDefault();
    const target = activateTarget.trim();
    if (!target) {
      setErrorMessage("Bundle version id is required to activate.");
      return;
    }

    setActivating(true);
    setErrorMessage("");
    try {
      await activateVersion(scope, target);
      setActivateTarget("");
      await loadConsoleData();
    } catch (error) {
      setErrorMessage(
        error instanceof Error ? error.message : "Activate operation failed.",
      );
    } finally {
      setActivating(false);
    }
  };

  const handleAddBinding = () => {
    const subject = newSubject.trim();
    if (!subject) {
      setErrorMessage("Policy subject is required.");
      return;
    }

    setErrorMessage("");
    setDraftBindings((previous) => {
      const existingIndex = previous.findIndex(
        (binding) => binding.subject === subject,
      );
      if (existingIndex >= 0) {
        const next = [...previous];
        next[existingIndex] = { subject, role: newRole };
        return next;
      }
      return [...previous, { subject, role: newRole }];
    });
    setNewSubject("");
    setNewRole(ThenvRole.Reader);
  };

  const handleRemoveBinding = (subject: string) => {
    setDraftBindings((previous) =>
      previous.filter((binding) => binding.subject !== subject),
    );
  };

  const handleSavePolicy = async () => {
    setSavingPolicy(true);
    setErrorMessage("");
    try {
      const response = await setPolicy(scope, draftBindings);
      setBindings(response.bindings);
      setDraftBindings(response.bindings);
      setPolicyRevision(response.policyRevision);
      await loadConsoleData();
    } catch (error) {
      setErrorMessage(
        error instanceof Error ? error.message : "Save policy request failed.",
      );
    } finally {
      setSavingPolicy(false);
    }
  };

  return (
    <section aria-label="thenv metadata console">
      <h2 style={{ marginTop: 0 }}>Thenv Metadata Console</h2>
      <p>
        This console is metadata-only. It does not render plaintext secret values
        from bundle payloads.
      </p>

      <form onSubmit={handleRefresh} style={{ marginBottom: "1.25rem" }}>
        <fieldset style={{ border: "1px solid #d7e2ea", padding: "0.75rem" }}>
          <legend>Scope</legend>
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
              gap: "0.75rem",
            }}
          >
            <label>
              Workspace
              <input
                value={scope.workspaceId}
                onChange={(event) =>
                  handleScopeChange("workspaceId", event.target.value)
                }
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Project
              <input
                value={scope.projectId}
                onChange={(event) =>
                  handleScopeChange("projectId", event.target.value)
                }
                style={{ width: "100%" }}
              />
            </label>
            <label>
              Environment
              <input
                value={scope.environmentId}
                onChange={(event) =>
                  handleScopeChange("environmentId", event.target.value)
                }
                style={{ width: "100%" }}
              />
            </label>
          </div>
          <div style={{ marginTop: "0.75rem", display: "flex", gap: "0.5rem" }}>
            <button type="submit" disabled={loading}>
              {loading ? "Loading..." : "Refresh"}
            </button>
            <span>Active version: {activeVersion || "(none)"}</span>
          </div>
        </fieldset>
      </form>

      {errorMessage ? (
        <p role="alert" style={{ color: "#9f1111" }}>
          {errorMessage}
        </p>
      ) : null}

      <section aria-label="version inventory" style={{ marginBottom: "1.5rem" }}>
        <h3>Version Inventory</h3>
        {versions.length === 0 ? (
          <p>No bundle versions were found for this scope.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr>
                <th align="left">Bundle Version</th>
                <th align="left">Status</th>
                <th align="left">Created By</th>
                <th align="left">Created At</th>
                <th align="left">File Types</th>
              </tr>
            </thead>
            <tbody>
              {versions.map((version) => (
                <tr key={version.bundleVersionId}>
                  <td>{version.bundleVersionId}</td>
                  <td>{version.status}</td>
                  <td>{version.createdBy}</td>
                  <td>{formatTimestamp(version.createdAt)}</td>
                  <td>{version.fileTypes.join(", ") || "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section aria-label="active version switch" style={{ marginBottom: "1.5rem" }}>
        <h3>Active Version Switch</h3>
        <form onSubmit={handleActivate}>
          <label>
            Target bundle version id
            <input
              value={activateTarget}
              onChange={(event) => setActivateTarget(event.target.value)}
              style={{ width: "100%", maxWidth: "420px", display: "block" }}
            />
          </label>
          <button type="submit" style={{ marginTop: "0.5rem" }} disabled={activating}>
            {activating ? "Activating..." : "Activate Version"}
          </button>
        </form>
      </section>

      <section aria-label="policy bindings" style={{ marginBottom: "1.5rem" }}>
        <h3>Policy Bindings</h3>
        <p>Current revision: {policyRevision}</p>

        <div style={{ marginBottom: "0.75rem" }}>
          <label>
            Subject
            <input
              value={newSubject}
              onChange={(event) => setNewSubject(event.target.value)}
              style={{ marginLeft: "0.5rem" }}
            />
          </label>
          <label style={{ marginLeft: "0.75rem" }}>
            Role
            <select
              value={newRole}
              onChange={(event) => setNewRole(event.target.value as ThenvRole)}
              style={{ marginLeft: "0.5rem" }}
            >
              {ROLE_OPTIONS.map((role) => (
                <option key={role} value={role}>
                  {roleLabel(role)}
                </option>
              ))}
            </select>
          </label>
          <button type="button" onClick={handleAddBinding} style={{ marginLeft: "0.75rem" }}>
            Add Binding
          </button>
        </div>

        {draftBindings.length === 0 ? (
          <p>No policy bindings configured.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr>
                <th align="left">Subject</th>
                <th align="left">Role</th>
                <th align="left">Actions</th>
              </tr>
            </thead>
            <tbody>
              {draftBindings.map((binding) => (
                <tr key={binding.subject}>
                  <td>{binding.subject}</td>
                  <td>{roleLabel(binding.role)}</td>
                  <td>
                    <button
                      type="button"
                      onClick={() => handleRemoveBinding(binding.subject)}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        <button
          type="button"
          onClick={handleSavePolicy}
          disabled={savingPolicy}
          style={{ marginTop: "0.75rem" }}
        >
          {savingPolicy ? "Saving..." : "Save Policy"}
        </button>
      </section>

      <section aria-label="audit events">
        <h3>Audit Events</h3>
        {auditEvents.length === 0 ? (
          <p>No audit events were found for this scope.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr>
                <th align="left">Event</th>
                <th align="left">Actor</th>
                <th align="left">Bundle</th>
                <th align="left">Target</th>
                <th align="left">Request</th>
                <th align="left">Created At</th>
              </tr>
            </thead>
            <tbody>
              {auditEvents.map((event) => (
                <tr key={event.eventId}>
                  <td>{event.eventType}</td>
                  <td>{event.actor}</td>
                  <td>{event.bundleVersionId || "-"}</td>
                  <td>{event.targetBundleVersionId || "-"}</td>
                  <td>{event.requestId}</td>
                  <td>{formatTimestamp(event.createdAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <p style={{ marginTop: "1.5rem", fontSize: "0.875rem", color: "#3f4f63" }}>
        Plaintext secret payloads are never shown in this UI.
      </p>

      {bindings.length > 0 ? (
        <p style={{ marginTop: "0.5rem", fontSize: "0.875rem", color: "#3f4f63" }}>
          Loaded {bindings.length} persisted binding(s).
        </p>
      ) : null}
    </section>
  );
}
