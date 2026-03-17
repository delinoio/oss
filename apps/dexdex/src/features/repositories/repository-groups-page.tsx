/**
 * Repository groups management page.
 */

import { type CSSProperties, useState } from "react";
import {
  useCreateRepositoryGroupMutation,
  useDeleteRepositoryGroupMutation,
  useListRepositories,
  useListRepositoryGroups,
  useUpdateRepositoryGroupMutation,
} from "../../hooks/use-dexdex-queries";
import { useAppStore } from "../../stores/app-store";

interface EditableGroupMember {
  repositoryId: string;
  branchRef: string;
}

export function RepositoryGroupsPage() {
  const { activeWorkspaceId } = useAppStore();

  const repositoriesQuery = useListRepositories(activeWorkspaceId);
  const repositoryGroupsQuery = useListRepositoryGroups(activeWorkspaceId);
  const createRepositoryGroupMutation = useCreateRepositoryGroupMutation();
  const updateRepositoryGroupMutation = useUpdateRepositoryGroupMutation();
  const deleteRepositoryGroupMutation = useDeleteRepositoryGroupMutation();

  const [editingGroupId, setEditingGroupId] = useState("");
  const [groupIdInput, setGroupIdInput] = useState("");
  const [groupMembers, setGroupMembers] = useState<EditableGroupMember[]>([{ repositoryId: "", branchRef: "main" }]);
  const [groupFormError, setGroupFormError] = useState("");

  const repositories = repositoriesQuery.data?.repositories ?? [];
  const repositoryGroups = repositoryGroupsQuery.data?.repositoryGroups ?? [];

  function selectGroupForEdit(repositoryGroupId: string) {
    setEditingGroupId(repositoryGroupId);
    setGroupFormError("");
    if (!repositoryGroupId) {
      setGroupIdInput("");
      setGroupMembers([{ repositoryId: "", branchRef: "main" }]);
      return;
    }

    const group = repositoryGroups.find((item) => item.repositoryGroupId === repositoryGroupId);
    if (!group) return;

    setGroupIdInput(group.repositoryGroupId);
    setGroupMembers(
      [...group.members]
        .sort((a, b) => a.displayOrder - b.displayOrder)
        .map((member) => ({ repositoryId: member.repositoryId, branchRef: member.branchRef || "main" })),
    );
  }

  function resetGroupEditor() {
    setEditingGroupId("");
    setGroupIdInput("");
    setGroupMembers([{ repositoryId: "", branchRef: "main" }]);
    setGroupFormError("");
  }

  function addGroupMemberRow() {
    setGroupMembers((prev) => [...prev, { repositoryId: "", branchRef: "main" }]);
  }

  function updateGroupMember(index: number, patch: Partial<EditableGroupMember>) {
    setGroupMembers((prev) => prev.map((member, i) => (i === index ? { ...member, ...patch } : member)));
  }

  function removeGroupMember(index: number) {
    setGroupMembers((prev) => prev.filter((_, i) => i !== index));
  }

  function moveGroupMember(index: number, direction: -1 | 1) {
    const nextIndex = index + direction;
    if (nextIndex < 0 || nextIndex >= groupMembers.length) return;
    setGroupMembers((prev) => {
      const copy = [...prev];
      const current = copy[index];
      copy[index] = copy[nextIndex];
      copy[nextIndex] = current;
      return copy;
    });
  }

  function handleSaveRepositoryGroup() {
    const repositoryGroupId = groupIdInput.trim();
    if (!repositoryGroupId) {
      setGroupFormError("Repository group ID is required.");
      return;
    }

    const normalizedMembers = groupMembers
      .map((member) => ({
        repositoryId: member.repositoryId.trim(),
        branchRef: member.branchRef.trim() || "main",
      }))
      .filter((member) => member.repositoryId.length > 0);

    if (normalizedMembers.length === 0) {
      setGroupFormError("At least one repository member is required.");
      return;
    }

    const dedupe = new Set<string>();
    for (const member of normalizedMembers) {
      if (dedupe.has(member.repositoryId)) {
        setGroupFormError("Duplicate repository IDs are not allowed.");
        return;
      }
      dedupe.add(member.repositoryId);
    }

    setGroupFormError("");
    const members = normalizedMembers.map((member, index) => ({
      repositoryId: member.repositoryId,
      branchRef: member.branchRef,
      displayOrder: index,
    }));

    if (editingGroupId) {
      updateRepositoryGroupMutation.mutate(
        {
          workspaceId: activeWorkspaceId,
          repositoryGroupId: editingGroupId,
          members,
        },
        {
          onSuccess: () => {
            resetGroupEditor();
          },
        },
      );
      return;
    }

    createRepositoryGroupMutation.mutate(
      {
        workspaceId: activeWorkspaceId,
        repositoryGroupId,
        members,
      },
      {
        onSuccess: () => {
          resetGroupEditor();
        },
      },
    );
  }

  function handleDeleteRepositoryGroup(repositoryGroupId: string) {
    deleteRepositoryGroupMutation.mutate({
      workspaceId: activeWorkspaceId,
      repositoryGroupId,
    });
    if (editingGroupId === repositoryGroupId) {
      resetGroupEditor();
    }
  }

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    flexShrink: 0,
  };

  const contentStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
    padding: "var(--space-6)",
  };

  const sectionStyle: CSSProperties = {
    marginBottom: "var(--space-8)",
  };

  const sectionTitleStyle: CSSProperties = {
    fontSize: "var(--font-size-md)",
    fontWeight: 600,
    marginBottom: "var(--space-4)",
    color: "var(--color-text-primary)",
  };

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-sm)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
  };

  return (
    <div style={containerStyle} data-testid="repository-groups-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Repository Groups</h1>
      </div>

      <div style={contentStyle}>
        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Repository Group Editor</h2>
          <div style={{ marginBottom: "var(--space-3)", display: "grid", gap: "var(--space-2)", maxWidth: 640 }}>
            <select
              style={inputStyle}
              value={editingGroupId}
              onChange={(e) => selectGroupForEdit(e.target.value)}
              data-testid="group-edit-select"
            >
              <option value="">Create new group</option>
              {repositoryGroups.map((group) => (
                <option key={group.repositoryGroupId} value={group.repositoryGroupId}>
                  {group.repositoryGroupId}
                </option>
              ))}
            </select>
            <input
              style={inputStyle}
              value={groupIdInput}
              onChange={(e) => setGroupIdInput(e.target.value)}
              placeholder="repository-group-id"
              disabled={editingGroupId.length > 0}
              data-testid="group-id-input"
            />
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)", maxWidth: 760 }}>
            {groupMembers.map((member, index) => (
              <div
                key={`${member.repositoryId}-${index}`}
                style={{
                  display: "grid",
                  gridTemplateColumns: "1fr 180px auto auto auto",
                  gap: "var(--space-2)",
                  alignItems: "center",
                }}
              >
                <select
                  style={inputStyle}
                  value={member.repositoryId}
                  onChange={(e) => updateGroupMember(index, { repositoryId: e.target.value })}
                >
                  <option value="">Select repository</option>
                  {repositories.map((repository) => (
                    <option key={repository.repositoryId} value={repository.repositoryId}>
                      {repository.repositoryId}
                    </option>
                  ))}
                </select>
                <input
                  style={inputStyle}
                  value={member.branchRef}
                  onChange={(e) => updateGroupMember(index, { branchRef: e.target.value })}
                  placeholder="branch"
                />
                <button style={secondaryButtonStyle} onClick={() => moveGroupMember(index, -1)}>
                  ↑
                </button>
                <button style={secondaryButtonStyle} onClick={() => moveGroupMember(index, 1)}>
                  ↓
                </button>
                <button style={dangerButtonStyle} onClick={() => removeGroupMember(index)} disabled={groupMembers.length <= 1}>
                  Remove
                </button>
              </div>
            ))}
          </div>

          {groupFormError && (
            <div style={{ marginTop: "var(--space-2)", color: "var(--color-status-failed)", fontSize: "var(--font-size-sm)" }}>
              {groupFormError}
            </div>
          )}

          <div style={{ marginTop: "var(--space-3)", display: "flex", gap: "var(--space-2)" }}>
            <button style={secondaryButtonStyle} onClick={addGroupMemberRow}>
              Add Member
            </button>
            <button style={primaryButtonStyle} onClick={handleSaveRepositoryGroup}>
              {editingGroupId ? "Update Group" : "Create Group"}
            </button>
            <button style={secondaryButtonStyle} onClick={resetGroupEditor}>
              Reset
            </button>
          </div>
        </div>

        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Repository Groups</h2>
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
            {repositoryGroups.map((group) => (
              <div
                key={group.repositoryGroupId}
                style={{
                  border: "1px solid var(--color-border)",
                  borderRadius: "var(--radius-md)",
                  padding: "var(--space-3)",
                }}
              >
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "var(--space-2)" }}>
                  <strong style={{ fontSize: "var(--font-size-sm)" }}>{group.repositoryGroupId}</strong>
                  <div style={{ display: "flex", gap: "var(--space-2)" }}>
                    <button style={secondaryButtonStyle} onClick={() => selectGroupForEdit(group.repositoryGroupId)}>
                      Edit
                    </button>
                    <button style={dangerButtonStyle} onClick={() => handleDeleteRepositoryGroup(group.repositoryGroupId)}>
                      Delete
                    </button>
                  </div>
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-1)" }}>
                  {[...group.members]
                    .sort((a, b) => a.displayOrder - b.displayOrder)
                    .map((member) => (
                      <div key={`${group.repositoryGroupId}-${member.repositoryId}-${member.displayOrder}`} style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-secondary)" }}>
                        #{member.displayOrder + 1} {member.repositoryId} ({member.branchRef || "main"})
                      </div>
                    ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

const primaryButtonStyle: CSSProperties = {
  padding: "var(--space-2) var(--space-3)",
  borderRadius: "var(--radius-md)",
  border: "1px solid var(--color-accent)",
  backgroundColor: "var(--color-accent)",
  color: "var(--color-text-inverse)",
  fontSize: "var(--font-size-sm)",
  fontWeight: 500,
  cursor: "pointer",
};

const secondaryButtonStyle: CSSProperties = {
  padding: "var(--space-2) var(--space-3)",
  borderRadius: "var(--radius-md)",
  border: "1px solid var(--color-border)",
  backgroundColor: "var(--color-bg-primary)",
  color: "var(--color-text-secondary)",
  fontSize: "var(--font-size-sm)",
  fontWeight: 500,
  cursor: "pointer",
};

const dangerButtonStyle: CSSProperties = {
  ...secondaryButtonStyle,
  border: "1px solid var(--color-status-failed)",
  color: "var(--color-status-failed)",
};
