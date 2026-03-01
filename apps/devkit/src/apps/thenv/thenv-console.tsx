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
  ThenvOutcome,
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

function outcomeLabel(outcome: ThenvOutcome): string {
  switch (outcome) {
    case ThenvOutcome.Success:
      return "Success";
    case ThenvOutcome.Denied:
      return "Denied";
    case ThenvOutcome.Failed:
      return "Failed";
    case ThenvOutcome.Unspecified:
      return "Unspecified";
    default:
      return "Unspecified";
  }
}

function outcomeBadgeClass(outcome: ThenvOutcome): string {
  switch (outcome) {
    case ThenvOutcome.Success:
      return "dk-thenv-outcome-success";
    case ThenvOutcome.Denied:
      return "dk-thenv-outcome-denied";
    case ThenvOutcome.Failed:
      return "dk-thenv-outcome-failed";
    case ThenvOutcome.Unspecified:
      return "dk-thenv-outcome-unspecified";
    default:
      return "dk-thenv-outcome-unspecified";
  }
}

export function ThenvConsole() {
  const [scope, setScope] = useState<ThenvScope>(DEFAULT_THENV_SCOPE);
  const [versions, setVersions] = useState<ThenvBundleVersionSummary[]>([]);
  const [versionsNextCursor, setVersionsNextCursor] = useState<string>("");
  const [auditEvents, setAuditEvents] = useState<ThenvAuditEvent[]>([]);
  const [auditNextCursor, setAuditNextCursor] = useState<string>("");
  const [bindings, setBindings] = useState<ThenvPolicyBinding[]>([]);
  const [policyRevision, setPolicyRevision] = useState<number>(0);

  const [activateTarget, setActivateTarget] = useState<string>("");
  const [newSubject, setNewSubject] = useState<string>("");
  const [newRole, setNewRole] = useState<ThenvRole>(ThenvRole.Reader);
  const [draftBindings, setDraftBindings] = useState<ThenvPolicyBinding[]>([]);
  const [auditFromTimeInput, setAuditFromTimeInput] = useState<string>("");
  const [auditToTimeInput, setAuditToTimeInput] = useState<string>("");
  const [appliedAuditFromTime, setAppliedAuditFromTime] = useState<string>("");
  const [appliedAuditToTime, setAppliedAuditToTime] = useState<string>("");

  const [loading, setLoading] = useState<boolean>(false);
  const [loadingMoreVersions, setLoadingMoreVersions] = useState<boolean>(false);
  const [loadingMoreAuditEvents, setLoadingMoreAuditEvents] = useState<boolean>(false);
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

  const loadAuditEvents = useCallback(async (fromTime: string, toTime: string) => {
    setLoading(true);
    setErrorMessage("");
    setAuditNextCursor("");

    try {
      const auditResponse = await listAuditEvents({
        scope,
        fromTime: fromTime || undefined,
        toTime: toTime || undefined,
      });
      setAuditEvents(auditResponse.events);
      setAuditNextCursor(auditResponse.nextCursor ?? "");

      logInfo({
        event: LogEvent.RouteRender,
        route: "/apps/thenv",
        message: "Loaded thenv audit events.",
        context: {
          auditFromTime: fromTime || undefined,
          auditToTime: toTime || undefined,
          nextCursor: auditResponse.nextCursor || undefined,
          loadedEventCount: auditResponse.events.length,
        },
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Failed to load thenv audit data.";
      setErrorMessage(message);
      logError({
        event: LogEvent.RouteLoadError,
        route: "/apps/thenv",
        message,
        error,
        context: {
          auditFromTime: fromTime || undefined,
          auditToTime: toTime || undefined,
        },
      });
    } finally {
      setLoading(false);
    }
  }, [scope]);

  const loadConsoleData = useCallback(async (fromTime: string, toTime: string) => {
    setLoading(true);
    setErrorMessage("");
    setVersionsNextCursor("");
    setAuditNextCursor("");

    try {
      const [versionsResponse, policyResponse, auditResponse] = await Promise.all([
        listVersions(scope),
        getPolicy(scope),
        listAuditEvents({
          scope,
          fromTime: fromTime || undefined,
          toTime: toTime || undefined,
        }),
      ]);

      setVersions(versionsResponse.versions);
      setVersionsNextCursor(versionsResponse.nextCursor ?? "");
      setBindings(policyResponse.bindings);
      setPolicyRevision(policyResponse.policyRevision);
      setDraftBindings(policyResponse.bindings);
      setAuditEvents(auditResponse.events);
      setAuditNextCursor(auditResponse.nextCursor ?? "");

      logInfo({
        event: LogEvent.RouteRender,
        route: "/apps/thenv",
        message: "Loaded thenv metadata console state.",
        context: {
          auditFromTime: fromTime || undefined,
          auditToTime: toTime || undefined,
          versionsNextCursor: versionsResponse.nextCursor || undefined,
          auditNextCursor: auditResponse.nextCursor || undefined,
          loadedVersionCount: versionsResponse.versions.length,
          loadedAuditEventCount: auditResponse.events.length,
        },
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
        context: {
          auditFromTime: fromTime || undefined,
          auditToTime: toTime || undefined,
        },
      });
    } finally {
      setLoading(false);
    }
  }, [scope]);

  useEffect(() => {
    void loadConsoleData(appliedAuditFromTime, appliedAuditToTime);
  }, [scope]);

  const handleScopeChange = (key: keyof ThenvScope, value: string) => {
    setScope((previous) => ({ ...previous, [key]: value }));
  };

  const handleRefresh = (event: FormEvent) => {
    event.preventDefault();
    void loadConsoleData(appliedAuditFromTime, appliedAuditToTime);
  };

  const handleApplyAuditFilters = (event: FormEvent) => {
    event.preventDefault();
    const nextFromTime = auditFromTimeInput.trim();
    const nextToTime = auditToTimeInput.trim();
    setAppliedAuditFromTime(nextFromTime);
    setAppliedAuditToTime(nextToTime);
    void loadAuditEvents(nextFromTime, nextToTime);
  };

  const handleClearAuditFilters = () => {
    setAuditFromTimeInput("");
    setAuditToTimeInput("");
    setAppliedAuditFromTime("");
    setAppliedAuditToTime("");
    void loadAuditEvents("", "");
  };

  const handleLoadMoreVersions = async () => {
    if (!versionsNextCursor) {
      return;
    }

    const cursor = versionsNextCursor;
    setLoadingMoreVersions(true);
    setErrorMessage("");
    try {
      const response = await listVersions(scope, { cursor });
      setVersions((previous) => [...previous, ...response.versions]);
      setVersionsNextCursor(response.nextCursor ?? "");
      logInfo({
        event: LogEvent.RouteRender,
        route: "/apps/thenv",
        message: "Loaded additional thenv bundle versions.",
        context: {
          cursor,
          nextCursor: response.nextCursor || undefined,
          loadedVersionCount: response.versions.length,
        },
      });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : "Failed to load additional versions.";
      setErrorMessage(message);
      logError({
        event: LogEvent.RouteLoadError,
        route: "/apps/thenv",
        message,
        error,
        context: { cursor },
      });
    } finally {
      setLoadingMoreVersions(false);
    }
  };

  const handleLoadMoreAuditEvents = async () => {
    if (!auditNextCursor) {
      return;
    }

    const cursor = auditNextCursor;
    setLoadingMoreAuditEvents(true);
    setErrorMessage("");
    try {
      const response = await listAuditEvents({
        scope,
        fromTime: appliedAuditFromTime || undefined,
        toTime: appliedAuditToTime || undefined,
        cursor,
      });
      setAuditEvents((previous) => [...previous, ...response.events]);
      setAuditNextCursor(response.nextCursor ?? "");
      logInfo({
        event: LogEvent.RouteRender,
        route: "/apps/thenv",
        message: "Loaded additional thenv audit events.",
        context: {
          cursor,
          nextCursor: response.nextCursor || undefined,
          loadedAuditEventCount: response.events.length,
          auditFromTime: appliedAuditFromTime || undefined,
          auditToTime: appliedAuditToTime || undefined,
        },
      });
    } catch (error) {
      const message =
        error instanceof Error
          ? error.message
          : "Failed to load additional audit events.";
      setErrorMessage(message);
      logError({
        event: LogEvent.RouteLoadError,
        route: "/apps/thenv",
        message,
        error,
        context: {
          cursor,
          auditFromTime: appliedAuditFromTime || undefined,
          auditToTime: appliedAuditToTime || undefined,
        },
      });
    } finally {
      setLoadingMoreAuditEvents(false);
    }
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
      await loadConsoleData(appliedAuditFromTime, appliedAuditToTime);
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
      await loadConsoleData(appliedAuditFromTime, appliedAuditToTime);
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
        {versionsNextCursor ? (
          <div className="dk-button-group">
            <button
              type="button"
              className="dk-button dk-button-secondary"
              onClick={handleLoadMoreVersions}
              disabled={loading || loadingMoreVersions}
            >
              {loadingMoreVersions ? "Loading More..." : "Load More Versions"}
            </button>
          </div>
        ) : null}
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
        <form onSubmit={handleApplyAuditFilters} className="dk-stack">
          <div className="dk-form-grid">
            <label className="dk-field">
              From Time (ISO)
              <input
                className="dk-input"
                value={auditFromTimeInput}
                onChange={(event) => setAuditFromTimeInput(event.target.value)}
                placeholder="2026-01-01T00:00:00Z"
              />
            </label>
            <label className="dk-field">
              To Time (ISO)
              <input
                className="dk-input"
                value={auditToTimeInput}
                onChange={(event) => setAuditToTimeInput(event.target.value)}
                placeholder="2026-01-31T23:59:59Z"
              />
            </label>
          </div>
          <div className="dk-button-group">
            <button type="submit" className="dk-button" disabled={loading}>
              Apply Audit Filters
            </button>
            <button
              type="button"
              className="dk-button dk-button-secondary"
              onClick={handleClearAuditFilters}
              disabled={loading}
            >
              Clear Audit Filters
            </button>
            <span className="dk-subtle">
              Applied range:
              {" "}
              {appliedAuditFromTime || "-"}
              {" to "}
              {appliedAuditToTime || "-"}
            </span>
          </div>
        </form>
        {auditEvents.length === 0 ? (
          <p className="dk-empty">No audit events were found for this scope.</p>
        ) : (
          <div className="dk-table-wrap">
            <table className="dk-table">
              <thead>
                <tr>
                  <th>Event</th>
                  <th>Outcome</th>
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
                    <td>
                      <span
                        className={`dk-thenv-outcome-badge ${outcomeBadgeClass(event.outcome)}`}
                      >
                        {outcomeLabel(event.outcome)}
                      </span>
                    </td>
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
        {auditNextCursor ? (
          <div className="dk-button-group">
            <button
              type="button"
              className="dk-button dk-button-secondary"
              onClick={handleLoadMoreAuditEvents}
              disabled={loading || loadingMoreAuditEvents}
            >
              {loadingMoreAuditEvents ? "Loading More..." : "Load More Audit Events"}
            </button>
          </div>
        ) : null}
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
