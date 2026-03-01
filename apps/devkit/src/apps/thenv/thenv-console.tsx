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
        listAuditEvents({ scope }),
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
    <section aria-label="thenv metadata console" className="dk-stack">
      <div className="dk-card">
        <p className="dk-eyebrow">Metadata Console</p>
        <h2 className="dk-section-title">Thenv Metadata Console</h2>
        <p className="dk-paragraph">
          This console is metadata-only. It does not render plaintext secret values
          from bundle payloads.
        </p>
      </div>

      <form onSubmit={handleRefresh} className="dk-card">
        <fieldset className="dk-fieldset">
          <legend className="dk-fieldset-legend">Scope</legend>
          <div className="dk-form-grid">
            <label className="dk-field">
              Workspace
              <input
                className="dk-input"
                value={scope.workspaceId}
                onChange={(event) =>
                  handleScopeChange("workspaceId", event.target.value)
                }
              />
            </label>
            <label className="dk-field">
              Project
              <input
                className="dk-input"
                value={scope.projectId}
                onChange={(event) =>
                  handleScopeChange("projectId", event.target.value)
                }
              />
            </label>
            <label className="dk-field">
              Environment
              <input
                className="dk-input"
                value={scope.environmentId}
                onChange={(event) =>
                  handleScopeChange("environmentId", event.target.value)
                }
              />
            </label>
          </div>

          <div className="dk-button-group">
            <button type="submit" className="dk-button" disabled={loading}>
              {loading ? "Loading..." : "Refresh"}
            </button>
            <span className="dk-subtle">
              Active version: <code className="dk-mono">{activeVersion || "(none)"}</code>
            </span>
          </div>
        </fieldset>
      </form>

      {errorMessage ? (
        <p role="alert" className="dk-alert">
          {errorMessage}
        </p>
      ) : null}

      <section aria-label="version inventory" className="dk-card">
        <h3 className="dk-subsection-title">Version Inventory</h3>
        {versions.length === 0 ? (
          <p className="dk-empty">No bundle versions were found for this scope.</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table">
              <thead>
                <tr>
                  <th>Bundle Version</th>
                  <th>Status</th>
                  <th>Created By</th>
                  <th>Created At</th>
                  <th>File Types</th>
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
          </div>
        )}
      </section>

      <section aria-label="active version switch" className="dk-card">
        <h3 className="dk-subsection-title">Active Version Switch</h3>
        <form onSubmit={handleActivate} className="dk-stack">
          <label className="dk-field">
            Target bundle version id
            <input
              className="dk-input"
              value={activateTarget}
              onChange={(event) => setActivateTarget(event.target.value)}
            />
          </label>
          <div className="dk-button-group">
            <button type="submit" className="dk-button" disabled={activating}>
              {activating ? "Activating..." : "Activate Version"}
            </button>
          </div>
        </form>
      </section>

      <section aria-label="policy bindings" className="dk-card">
        <h3 className="dk-subsection-title">Policy Bindings</h3>
        <p className="dk-subtle">Current revision: {policyRevision}</p>

        <div className="dk-stack">
          <div className="dk-form-grid">
            <label className="dk-field">
              Subject
              <input
                className="dk-input"
                value={newSubject}
                onChange={(event) => setNewSubject(event.target.value)}
              />
            </label>
            <label className="dk-field">
              Role
              <select
                className="dk-select"
                value={newRole}
                onChange={(event) => setNewRole(event.target.value as ThenvRole)}
              >
                {ROLE_OPTIONS.map((role) => (
                  <option key={role} value={role}>
                    {roleLabel(role)}
                  </option>
                ))}
              </select>
            </label>
          </div>

          <div className="dk-button-group">
            <button
              type="button"
              className="dk-button dk-button-secondary"
              onClick={handleAddBinding}
            >
              Add Binding
            </button>
          </div>
        </div>

        {draftBindings.length === 0 ? (
          <p className="dk-empty">No policy bindings configured.</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table">
              <thead>
                <tr>
                  <th>Subject</th>
                  <th>Role</th>
                  <th>Actions</th>
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
                        className="dk-button dk-button-danger"
                        onClick={() => handleRemoveBinding(binding.subject)}
                      >
                        Remove
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <div className="dk-button-group">
          <button
            type="button"
            className="dk-button"
            onClick={handleSavePolicy}
            disabled={savingPolicy}
          >
            {savingPolicy ? "Saving..." : "Save Policy"}
          </button>
        </div>
      </section>

      <section aria-label="audit events" className="dk-card">
        <h3 className="dk-subsection-title">Audit Events</h3>
        {auditEvents.length === 0 ? (
          <p className="dk-empty">No audit events were found for this scope.</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table">
              <thead>
                <tr>
                  <th>Event</th>
                  <th>Actor</th>
                  <th>Bundle</th>
                  <th>Target</th>
                  <th>Request</th>
                  <th>Created At</th>
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
          </div>
        )}
      </section>

      <div className="dk-card dk-card-muted">
        <p className="dk-subtle">Plaintext secret payloads are never shown in this UI.</p>
        {bindings.length > 0 ? (
          <p className="dk-subtle">
            Loaded {bindings.length} persisted binding(s).
          </p>
        ) : null}
      </div>
    </section>
  );
}
