"use client";

import { useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import { AuditEventType, Outcome, type Scope } from "@/gen/thenv/v1/thenv_pb";
import { useListAuditEvents } from "@/apps/thenv/hooks/use-thenv-queries";

export interface AuditViewerProps {
  scope: Scope;
}

function eventTypeLabel(t: AuditEventType): string {
  switch (t) {
    case AuditEventType.PUSH: return "Push";
    case AuditEventType.PULL: return "Pull";
    case AuditEventType.LIST: return "List";
    case AuditEventType.ROTATE: return "Rotate";
    case AuditEventType.ACTIVATE: return "Activate";
    case AuditEventType.POLICY_UPDATE: return "Policy Update";
    default: return "All";
  }
}

function outcomeLabel(o: Outcome): string {
  switch (o) {
    case Outcome.SUCCESS: return "Success";
    case Outcome.DENIED: return "Denied";
    case Outcome.FAILED: return "Failed";
    default: return "-";
  }
}

function outcomeColor(o: Outcome): string {
  switch (o) {
    case Outcome.SUCCESS: return "#16a34a";
    case Outcome.DENIED: return "#ea580c";
    case Outcome.FAILED: return "#dc2626";
    default: return "#6b7280";
  }
}

const EVENT_TYPE_FILTERS = [
  AuditEventType.UNSPECIFIED,
  AuditEventType.PUSH,
  AuditEventType.PULL,
  AuditEventType.LIST,
  AuditEventType.ROTATE,
  AuditEventType.ACTIVATE,
  AuditEventType.POLICY_UPDATE,
];

export function AuditViewer({ scope }: AuditViewerProps) {
  const [filter, setFilter] = useState<AuditEventType>(AuditEventType.UNSPECIFIED);
  const { data, isLoading } = useListAuditEvents(scope, filter);

  const events = data?.events ?? [];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "1rem" }}>
        <h3 style={{ margin: 0 }}>Audit Log</h3>
        <select
          value={filter}
          onChange={(e) => setFilter(Number(e.target.value) as AuditEventType)}
          style={{
            padding: "0.3rem 0.5rem",
            border: "1px solid #d7e2ea",
            borderRadius: "6px",
            fontSize: "0.8rem",
          }}
        >
          {EVENT_TYPE_FILTERS.map((t) => (
            <option key={t} value={t}>
              {eventTypeLabel(t)}
            </option>
          ))}
        </select>
      </div>

      {isLoading ? (
        <p style={{ color: "#64748b" }}>Loading audit events...</p>
      ) : events.length === 0 ? (
        <p style={{ color: "#64748b", fontSize: "0.875rem" }}>No audit events found.</p>
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.875rem" }}>
          <thead>
            <tr style={{ borderBottom: "2px solid #e2e8f0", textAlign: "left" }}>
              <th style={{ padding: "0.5rem" }}>Event</th>
              <th style={{ padding: "0.5rem" }}>Actor</th>
              <th style={{ padding: "0.5rem" }}>Outcome</th>
              <th style={{ padding: "0.5rem" }}>Version ID</th>
              <th style={{ padding: "0.5rem" }}>Time</th>
            </tr>
          </thead>
          <tbody>
            {events.map((ev) => (
              <tr key={ev.eventId} style={{ borderBottom: "1px solid #f1f5f9" }}>
                <td style={{ padding: "0.5rem" }}>{eventTypeLabel(ev.eventType)}</td>
                <td style={{ padding: "0.5rem", fontFamily: "monospace", fontSize: "0.8rem" }}>
                  {ev.actor || "-"}
                </td>
                <td style={{ padding: "0.5rem" }}>
                  <span style={{ color: outcomeColor(ev.outcome), fontWeight: 500 }}>
                    {outcomeLabel(ev.outcome)}
                  </span>
                </td>
                <td style={{ padding: "0.5rem", fontFamily: "monospace", fontSize: "0.8rem" }}>
                  {ev.bundleVersionId ? ev.bundleVersionId.slice(0, 12) + "..." : "-"}
                </td>
                <td style={{ padding: "0.5rem", color: "#64748b" }}>
                  {ev.createdAt ? timestampDate(ev.createdAt).toLocaleString() : "-"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
