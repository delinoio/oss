"use client";

import { create } from "@bufbuild/protobuf";
import { useCallback, useState } from "react";

import {
  Role,
  type Scope,
  PolicyBindingSchema,
} from "@/gen/thenv/v1/thenv_pb";
import {
  useGetPolicy,
  useSetPolicyMutation,
} from "@/apps/thenv/hooks/use-thenv-queries";

export interface PolicyEditorProps {
  scope: Scope;
}

function roleLabel(role: Role): string {
  switch (role) {
    case Role.READER: return "Reader";
    case Role.WRITER: return "Writer";
    case Role.ADMIN: return "Admin";
    default: return "Unspecified";
  }
}

const ROLE_OPTIONS = [Role.READER, Role.WRITER, Role.ADMIN];

export function PolicyEditor({ scope }: PolicyEditorProps) {
  const { data, isLoading } = useGetPolicy(scope);
  const setMutation = useSetPolicyMutation();
  const [newSubject, setNewSubject] = useState("");
  const [newRole, setNewRole] = useState<Role>(Role.READER);

  const handleAdd = useCallback(() => {
    if (!newSubject.trim()) return;
    const existing = data?.bindings ?? [];
    const binding = create(PolicyBindingSchema, { subject: newSubject.trim(), role: newRole });
    setMutation.mutate({
      scope,
      bindings: [...existing, binding],
    });
    setNewSubject("");
  }, [newSubject, newRole, data, scope, setMutation]);

  const handleRemove = useCallback(
    (subject: string) => {
      const existing = data?.bindings ?? [];
      setMutation.mutate({
        scope,
        bindings: existing.filter((b) => b.subject !== subject),
      });
    },
    [data, scope, setMutation],
  );

  if (isLoading) {
    return <p style={{ color: "#64748b" }}>Loading policy...</p>;
  }

  const bindings = data?.bindings ?? [];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <div style={{ display: "flex", alignItems: "center", gap: "1rem" }}>
        <h3 style={{ margin: 0 }}>Access Policy</h3>
        {data?.policyRevision !== undefined && (
          <span style={{ fontSize: "0.75rem", color: "#94a3b8" }}>
            Revision: {data.policyRevision.toString()}
          </span>
        )}
      </div>

      {bindings.length === 0 ? (
        <p style={{ color: "#64748b", fontSize: "0.875rem" }}>No policy bindings configured.</p>
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.875rem" }}>
          <thead>
            <tr style={{ borderBottom: "2px solid #e2e8f0", textAlign: "left" }}>
              <th style={{ padding: "0.5rem" }}>Subject</th>
              <th style={{ padding: "0.5rem" }}>Role</th>
              <th style={{ padding: "0.5rem", width: "80px" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {bindings.map((b) => (
              <tr key={b.subject} style={{ borderBottom: "1px solid #f1f5f9" }}>
                <td style={{ padding: "0.5rem", fontFamily: "monospace" }}>{b.subject}</td>
                <td style={{ padding: "0.5rem" }}>{roleLabel(b.role)}</td>
                <td style={{ padding: "0.5rem" }}>
                  <button
                    onClick={() => handleRemove(b.subject)}
                    style={{
                      padding: "0.2rem 0.5rem",
                      backgroundColor: "#fee2e2",
                      color: "#dc2626",
                      border: "1px solid #fecaca",
                      borderRadius: "4px",
                      cursor: "pointer",
                      fontSize: "0.75rem",
                    }}
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div
        style={{
          display: "flex",
          gap: "0.5rem",
          alignItems: "flex-end",
          padding: "0.75rem",
          backgroundColor: "#f8fafc",
          borderRadius: "6px",
          border: "1px solid #e2e8f0",
        }}
      >
        <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
          Subject
          <input
            value={newSubject}
            onChange={(e) => setNewSubject(e.target.value)}
            placeholder="user@example.com"
            style={{
              padding: "0.4rem 0.6rem",
              border: "1px solid #d7e2ea",
              borderRadius: "6px",
              fontSize: "0.875rem",
              width: "200px",
            }}
          />
        </label>
        <label style={{ display: "flex", flexDirection: "column", gap: "0.25rem", fontSize: "0.75rem", color: "#64748b" }}>
          Role
          <select
            value={newRole}
            onChange={(e) => setNewRole(Number(e.target.value) as Role)}
            style={{
              padding: "0.4rem 0.6rem",
              border: "1px solid #d7e2ea",
              borderRadius: "6px",
              fontSize: "0.875rem",
            }}
          >
            {ROLE_OPTIONS.map((r) => (
              <option key={r} value={r}>
                {roleLabel(r)}
              </option>
            ))}
          </select>
        </label>
        <button
          onClick={handleAdd}
          disabled={setMutation.isPending || !newSubject.trim()}
          style={{
            padding: "0.4rem 0.75rem",
            backgroundColor: "#0c5fca",
            color: "#fff",
            border: "none",
            borderRadius: "6px",
            cursor: "pointer",
            fontSize: "0.8rem",
          }}
        >
          Add Binding
        </button>
      </div>
    </div>
  );
}
