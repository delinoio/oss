/**
 * Repositories management page.
 */

import { type CSSProperties, useEffect, useState } from "react";
import {
  useCreateRepositoryMutation,
  useDeleteRepositoryMutation,
  useListRepositories,
  useUpdateRepositoryMutation,
} from "../../hooks/use-dexdex-queries";
import { useAppStore } from "../../stores/app-store";

export function RepositoriesPage() {
  const { activeWorkspaceId } = useAppStore();

  const repositoriesQuery = useListRepositories(activeWorkspaceId);
  const createRepositoryMutation = useCreateRepositoryMutation();
  const updateRepositoryMutation = useUpdateRepositoryMutation();
  const deleteRepositoryMutation = useDeleteRepositoryMutation();

  const [newRepositoryUrl, setNewRepositoryUrl] = useState("");
  const [repositoryEdits, setRepositoryEdits] = useState<Record<string, string>>({});

  const repositories = repositoriesQuery.data?.repositories ?? [];

  useEffect(() => {
    if (repositories.length === 0) return;
    setRepositoryEdits((prev) => {
      const next = { ...prev };
      for (const repository of repositories) {
        if (!next[repository.repositoryId]) {
          next[repository.repositoryId] = repository.repositoryUrl;
        }
      }
      return next;
    });
  }, [repositories]);

  function handleCreateRepository() {
    const repositoryUrl = newRepositoryUrl.trim();
    if (!repositoryUrl) return;
    createRepositoryMutation.mutate(
      { workspaceId: activeWorkspaceId, repositoryUrl },
      {
        onSuccess: () => {
          setNewRepositoryUrl("");
        },
      },
    );
  }

  function handleUpdateRepository(repositoryId: string) {
    const repositoryUrl = (repositoryEdits[repositoryId] ?? "").trim();
    if (!repositoryUrl) return;
    updateRepositoryMutation.mutate({
      workspaceId: activeWorkspaceId,
      repositoryId,
      repositoryUrl,
    });
  }

  function handleDeleteRepository(repositoryId: string) {
    deleteRepositoryMutation.mutate({
      workspaceId: activeWorkspaceId,
      repositoryId,
    });
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
    <div style={containerStyle} data-testid="repositories-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Repositories</h1>
      </div>

      <div style={contentStyle}>
        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Create Repository</h2>
          <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: "var(--space-2)", maxWidth: 700 }}>
            <input
              style={inputStyle}
              type="text"
              value={newRepositoryUrl}
              onChange={(e) => setNewRepositoryUrl(e.target.value)}
              placeholder="https://github.com/org/repo"
              data-testid="create-repository-url"
            />
            <button style={primaryButtonStyle} onClick={handleCreateRepository} disabled={createRepositoryMutation.isPending}>
              Add Repository
            </button>
          </div>
        </div>

        <div style={sectionStyle}>
          <h2 style={sectionTitleStyle}>Repositories</h2>
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
            {repositories.map((repository) => (
              <div
                key={repository.repositoryId}
                style={{
                  display: "grid",
                  gridTemplateColumns: "180px 1fr auto auto",
                  gap: "var(--space-2)",
                  alignItems: "center",
                  padding: "var(--space-2)",
                  border: "1px solid var(--color-border)",
                  borderRadius: "var(--radius-md)",
                }}
              >
                <span style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-tertiary)" }}>{repository.repositoryId}</span>
                <input
                  style={inputStyle}
                  value={repositoryEdits[repository.repositoryId] ?? repository.repositoryUrl}
                  onChange={(e) =>
                    setRepositoryEdits((prev) => ({
                      ...prev,
                      [repository.repositoryId]: e.target.value,
                    }))
                  }
                />
                <button style={secondaryButtonStyle} onClick={() => handleUpdateRepository(repository.repositoryId)}>
                  Save
                </button>
                <button style={dangerButtonStyle} onClick={() => handleDeleteRepository(repository.repositoryId)}>
                  Delete
                </button>
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
