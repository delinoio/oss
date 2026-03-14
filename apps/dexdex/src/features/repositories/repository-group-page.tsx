/**
 * Repository group management page.
 * Lists all repository groups with create, edit, and delete capabilities.
 */

import { type CSSProperties, useState } from "react";
import {
  useListRepositoryGroups,
  useCreateRepositoryGroupMutation,
  useUpdateRepositoryGroupMutation,
  useDeleteRepositoryGroupMutation,
} from "../../hooks/use-dexdex-queries";

const WORKSPACE_ID = "workspace-default";

interface GroupFormState {
  repositoryGroupId: string;
  repositories: Array<{ repositoryId: string; branchRef: string }>;
}

const EMPTY_FORM: GroupFormState = {
  repositoryGroupId: "",
  repositories: [],
};

interface RepositoryGroupRecord {
  repositoryGroupId: string;
  repositories: Array<{
    repositoryId: string;
    repositoryUrl: string;
    branchRef: string;
  }>;
}

export function RepositoryGroupPage() {
  const [formState, setFormState] = useState<GroupFormState>(EMPTY_FORM);
  const [newRepoId, setNewRepoId] = useState("");
  const [newBranchRef, setNewBranchRef] = useState("main");
  const [editingId, setEditingId] = useState<string | null>(null);

  const groupsQuery = useListRepositoryGroups(WORKSPACE_ID);
  const groups = (groupsQuery.data?.repositoryGroups ?? []) as RepositoryGroupRecord[];

  const createMutation = useCreateRepositoryGroupMutation();
  const updateMutation = useUpdateRepositoryGroupMutation();
  const deleteMutation = useDeleteRepositoryGroupMutation();

  function handleAddRepo() {
    if (!newRepoId.trim()) return;
    setFormState((s) => ({
      ...s,
      repositories: [...s.repositories, { repositoryId: newRepoId.trim(), branchRef: newBranchRef.trim() || "main" }],
    }));
    setNewRepoId("");
    setNewBranchRef("main");
  }

  function handleRemoveRepo(index: number) {
    setFormState((s) => ({
      ...s,
      repositories: s.repositories.filter((_, i) => i !== index),
    }));
  }

  function handleCreate() {
    if (!formState.repositoryGroupId.trim()) return;
    createMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryGroupId: formState.repositoryGroupId.trim(),
      repositories: formState.repositories,
    });
    setFormState(EMPTY_FORM);
  }

  function handleUpdate(groupId: string) {
    updateMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryGroupId: groupId,
      repositories: formState.repositories,
    });
    setEditingId(null);
    setFormState(EMPTY_FORM);
  }

  function handleDelete(groupId: string) {
    deleteMutation.mutate({ workspaceId: WORKSPACE_ID, repositoryGroupId: groupId });
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

  const inputStyle: CSSProperties = {
    width: "100%",
    padding: "var(--space-2) var(--space-3)",
    borderRadius: "var(--radius-md)",
    border: "1px solid var(--color-border)",
    fontSize: "var(--font-size-base)",
    backgroundColor: "var(--color-bg-secondary)",
    color: "var(--color-text-primary)",
    outline: "none",
  };

  const labelStyle: CSSProperties = {
    display: "block",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    marginBottom: "var(--space-1)",
    color: "var(--color-text-secondary)",
  };

  const sectionStyle: CSSProperties = {
    marginBottom: "var(--space-8)",
  };

  const rowStyle: CSSProperties = {
    display: "flex",
    alignItems: "flex-start",
    gap: "var(--space-3)",
    padding: "var(--space-3)",
    borderBottom: "1px solid var(--color-border-subtle)",
  };

  const buttonStyle: CSSProperties = {
    padding: "var(--space-1) var(--space-3)",
    borderRadius: "var(--radius-md)",
    fontSize: "var(--font-size-sm)",
    cursor: "pointer",
    border: "1px solid var(--color-border)",
    color: "var(--color-text-secondary)",
  };

  const primaryButtonStyle: CSSProperties = {
    ...buttonStyle,
    backgroundColor: "var(--color-accent)",
    color: "var(--color-text-inverse)",
    border: "none",
  };

  const dangerButtonStyle: CSSProperties = {
    ...buttonStyle,
    color: "var(--color-error)",
    borderColor: "var(--color-error)",
  };

  function renderRepoForm() {
    return (
      <>
        {formState.repositories.map((repo, i) => (
          <div key={i} style={{ display: "flex", gap: "var(--space-2)", alignItems: "center", marginBottom: "var(--space-1)" }}>
            <span style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-primary)" }}>
              {repo.repositoryId} ({repo.branchRef})
            </span>
            <button style={{ ...buttonStyle, padding: "0 var(--space-2)" }} onClick={() => handleRemoveRepo(i)}>
              x
            </button>
          </div>
        ))}
        <div style={{ display: "flex", gap: "var(--space-2)", alignItems: "flex-end" }}>
          <div style={{ flex: 1 }}>
            <label style={labelStyle}>Repository ID</label>
            <input
              style={inputStyle}
              value={newRepoId}
              onChange={(e) => setNewRepoId(e.target.value)}
              placeholder="repo-id"
            />
          </div>
          <div style={{ width: "120px" }}>
            <label style={labelStyle}>Branch</label>
            <input
              style={inputStyle}
              value={newBranchRef}
              onChange={(e) => setNewBranchRef(e.target.value)}
              placeholder="main"
            />
          </div>
          <button style={buttonStyle} onClick={handleAddRepo}>
            Add
          </button>
        </div>
      </>
    );
  }

  return (
    <div style={containerStyle} data-testid="repository-group-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Repository Groups</h1>
      </div>
      <div style={contentStyle}>
        {/* Create Form */}
        <div style={sectionStyle}>
          <h2
            style={{
              fontSize: "var(--font-size-md)",
              fontWeight: 600,
              marginBottom: "var(--space-4)",
              color: "var(--color-text-primary)",
            }}
          >
            Create Repository Group
          </h2>
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)", maxWidth: "480px" }}>
            <div>
              <label style={labelStyle}>Group ID</label>
              <input
                style={inputStyle}
                type="text"
                value={formState.repositoryGroupId}
                onChange={(e) => setFormState((s) => ({ ...s, repositoryGroupId: e.target.value }))}
                placeholder="my-group"
                data-testid="group-id-input"
              />
            </div>
            <div>
              <label style={labelStyle}>Repositories</label>
              {renderRepoForm()}
            </div>
            <div>
              <button
                style={primaryButtonStyle}
                onClick={handleCreate}
                disabled={!formState.repositoryGroupId.trim()}
                data-testid="group-create-button"
              >
                Create Group
              </button>
            </div>
          </div>
        </div>

        {/* Group List */}
        <div style={sectionStyle}>
          <h2
            style={{
              fontSize: "var(--font-size-md)",
              fontWeight: 600,
              marginBottom: "var(--space-4)",
              color: "var(--color-text-primary)",
            }}
          >
            All Repository Groups
          </h2>
          {groups.length === 0 ? (
            <div
              style={{
                padding: "var(--space-6)",
                textAlign: "center",
                color: "var(--color-text-tertiary)",
                fontSize: "var(--font-size-sm)",
              }}
            >
              No repository groups found
            </div>
          ) : (
            groups.map((group) => (
              <div key={group.repositoryGroupId} style={rowStyle} data-testid={`group-row-${group.repositoryGroupId}`}>
                {editingId === group.repositoryGroupId ? (
                  <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
                    {renderRepoForm()}
                    <div style={{ display: "flex", gap: "var(--space-2)", marginTop: "var(--space-2)" }}>
                      <button style={primaryButtonStyle} onClick={() => handleUpdate(group.repositoryGroupId)}>
                        Save
                      </button>
                      <button style={buttonStyle} onClick={() => { setEditingId(null); setFormState(EMPTY_FORM); }}>
                        Cancel
                      </button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: "var(--font-size-base)", fontWeight: 500, color: "var(--color-text-primary)" }}>
                        {group.repositoryGroupId}
                      </div>
                      <div style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-tertiary)", marginTop: "var(--space-1)" }}>
                        {(group.repositories ?? []).length} repositories
                      </div>
                      {(group.repositories ?? []).map((repo) => (
                        <div
                          key={repo.repositoryId}
                          style={{
                            fontSize: "var(--font-size-xs)",
                            color: "var(--color-text-secondary)",
                            marginTop: "var(--space-1)",
                            paddingLeft: "var(--space-3)",
                          }}
                        >
                          {repo.repositoryUrl} ({repo.branchRef})
                        </div>
                      ))}
                    </div>
                    <button
                      style={buttonStyle}
                      onClick={() => {
                        setEditingId(group.repositoryGroupId);
                        setFormState({
                          repositoryGroupId: group.repositoryGroupId,
                          repositories: (group.repositories ?? []).map((r) => ({
                            repositoryId: r.repositoryId,
                            branchRef: r.branchRef,
                          })),
                        });
                      }}
                    >
                      Edit
                    </button>
                    <button
                      style={dangerButtonStyle}
                      onClick={() => handleDelete(group.repositoryGroupId)}
                    >
                      Delete
                    </button>
                  </>
                )}
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
