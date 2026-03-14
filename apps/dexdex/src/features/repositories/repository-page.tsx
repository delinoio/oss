/**
 * Repository management page.
 * Lists all repositories with create, edit, and delete capabilities.
 */

import { type CSSProperties, useState } from "react";
import {
  useListRepositories,
  useCreateRepositoryMutation,
  useUpdateRepositoryMutation,
  useDeleteRepositoryMutation,
} from "../../hooks/use-dexdex-queries";

const WORKSPACE_ID = "workspace-default";

interface RepositoryFormState {
  repositoryUrl: string;
  defaultBranchRef: string;
  displayName: string;
}

const EMPTY_FORM: RepositoryFormState = {
  repositoryUrl: "",
  defaultBranchRef: "main",
  displayName: "",
};

export function RepositoryPage() {
  const [formState, setFormState] = useState<RepositoryFormState>(EMPTY_FORM);
  const [editingId, setEditingId] = useState<string | null>(null);

  const reposQuery = useListRepositories(WORKSPACE_ID);
  const repositoryGroups = reposQuery.data?.repositoryGroups ?? [];

  // Flatten repositories from groups for display
  const allRepos: Array<{ repositoryId: string; repositoryUrl: string; branchRef: string; groupId: string }> = [];
  for (const group of repositoryGroups) {
    for (const repo of (group as { repositoryGroupId: string; repositories: Array<{ repositoryId: string; repositoryUrl: string; branchRef: string }> }).repositories ?? []) {
      allRepos.push({
        repositoryId: repo.repositoryId,
        repositoryUrl: repo.repositoryUrl,
        branchRef: repo.branchRef,
        groupId: (group as { repositoryGroupId: string }).repositoryGroupId,
      });
    }
  }

  const createMutation = useCreateRepositoryMutation();
  const updateMutation = useUpdateRepositoryMutation();
  const deleteMutation = useDeleteRepositoryMutation();

  function handleCreate() {
    if (!formState.repositoryUrl.trim()) return;
    createMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryUrl: formState.repositoryUrl.trim(),
      defaultBranchRef: formState.defaultBranchRef.trim() || "main",
      displayName: formState.displayName.trim(),
    });
    setFormState(EMPTY_FORM);
  }

  function handleUpdate(repositoryId: string) {
    updateMutation.mutate({
      workspaceId: WORKSPACE_ID,
      repositoryId,
      repositoryUrl: formState.repositoryUrl.trim(),
      defaultBranchRef: formState.defaultBranchRef.trim() || "main",
      displayName: formState.displayName.trim(),
    });
    setEditingId(null);
    setFormState(EMPTY_FORM);
  }

  function handleDelete(repositoryId: string) {
    deleteMutation.mutate({ workspaceId: WORKSPACE_ID, repositoryId });
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
    alignItems: "center",
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

  return (
    <div style={containerStyle} data-testid="repository-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Repositories</h1>
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
            Add Repository
          </h2>
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)", maxWidth: "480px" }}>
            <div>
              <label style={labelStyle}>Repository URL</label>
              <input
                style={inputStyle}
                type="text"
                value={formState.repositoryUrl}
                onChange={(e) => setFormState((s) => ({ ...s, repositoryUrl: e.target.value }))}
                placeholder="https://github.com/org/repo"
                data-testid="repo-url-input"
              />
            </div>
            <div>
              <label style={labelStyle}>Default Branch</label>
              <input
                style={inputStyle}
                type="text"
                value={formState.defaultBranchRef}
                onChange={(e) => setFormState((s) => ({ ...s, defaultBranchRef: e.target.value }))}
                placeholder="main"
                data-testid="repo-branch-input"
              />
            </div>
            <div>
              <label style={labelStyle}>Display Name</label>
              <input
                style={inputStyle}
                type="text"
                value={formState.displayName}
                onChange={(e) => setFormState((s) => ({ ...s, displayName: e.target.value }))}
                placeholder="My Repository"
                data-testid="repo-display-name-input"
              />
            </div>
            <div>
              <button
                style={primaryButtonStyle}
                onClick={handleCreate}
                disabled={!formState.repositoryUrl.trim()}
                data-testid="repo-create-button"
              >
                Add Repository
              </button>
            </div>
          </div>
        </div>

        {/* Repository List */}
        <div style={sectionStyle}>
          <h2
            style={{
              fontSize: "var(--font-size-md)",
              fontWeight: 600,
              marginBottom: "var(--space-4)",
              color: "var(--color-text-primary)",
            }}
          >
            All Repositories
          </h2>
          {allRepos.length === 0 ? (
            <div
              style={{
                padding: "var(--space-6)",
                textAlign: "center",
                color: "var(--color-text-tertiary)",
                fontSize: "var(--font-size-sm)",
              }}
            >
              No repositories found
            </div>
          ) : (
            allRepos.map((repo) => (
              <div key={repo.repositoryId} style={rowStyle} data-testid={`repo-row-${repo.repositoryId}`}>
                {editingId === repo.repositoryId ? (
                  <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
                    <input
                      style={inputStyle}
                      value={formState.repositoryUrl}
                      onChange={(e) => setFormState((s) => ({ ...s, repositoryUrl: e.target.value }))}
                    />
                    <input
                      style={inputStyle}
                      value={formState.defaultBranchRef}
                      onChange={(e) => setFormState((s) => ({ ...s, defaultBranchRef: e.target.value }))}
                    />
                    <input
                      style={inputStyle}
                      value={formState.displayName}
                      onChange={(e) => setFormState((s) => ({ ...s, displayName: e.target.value }))}
                    />
                    <div style={{ display: "flex", gap: "var(--space-2)" }}>
                      <button style={primaryButtonStyle} onClick={() => handleUpdate(repo.repositoryId)}>
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
                        {repo.repositoryUrl}
                      </div>
                      <div style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-tertiary)", marginTop: "var(--space-1)" }}>
                        Branch: {repo.branchRef} | Group: {repo.groupId}
                      </div>
                    </div>
                    <button
                      style={buttonStyle}
                      onClick={() => {
                        setEditingId(repo.repositoryId);
                        setFormState({
                          repositoryUrl: repo.repositoryUrl,
                          defaultBranchRef: repo.branchRef,
                          displayName: "",
                        });
                      }}
                    >
                      Edit
                    </button>
                    <button
                      style={dangerButtonStyle}
                      onClick={() => handleDelete(repo.repositoryId)}
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
