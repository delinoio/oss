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

function formatMutationError(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

function logRepositoryCreateStart(workspaceId: string, repositoryUrl: string) {
  console.info("[RepositoriesPage] create_repository:start", {
    workspaceId,
    repositoryUrl,
  });
}

function logRepositoryCreateSuccess(workspaceId: string, repositoryUrl: string, repositoryId: string) {
  console.info("[RepositoriesPage] create_repository:success", {
    workspaceId,
    repositoryUrl,
    repositoryId,
  });
}

function logRepositoryCreateFailure(workspaceId: string, repositoryUrl: string, errorMessage: string) {
  console.error("[RepositoriesPage] create_repository:failed", {
    workspaceId,
    repositoryUrl,
    error: errorMessage,
  });
}

export function RepositoriesPage() {
  const { activeWorkspaceId } = useAppStore();

  const repositoriesQuery = useListRepositories(activeWorkspaceId);
  const createRepositoryMutation = useCreateRepositoryMutation();
  const updateRepositoryMutation = useUpdateRepositoryMutation();
  const deleteRepositoryMutation = useDeleteRepositoryMutation();

  const [newRepositoryUrl, setNewRepositoryUrl] = useState("");
  const [repositoryEdits, setRepositoryEdits] = useState<Record<string, string>>({});
  const [repositoryMutationError, setRepositoryMutationError] = useState("");

  const repositories = repositoriesQuery.data?.repositories ?? [];
  const hasActiveWorkspace = activeWorkspaceId.trim().length > 0;

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
    if (createRepositoryMutation.isPending) {
      return;
    }

    if (!hasActiveWorkspace) {
      setRepositoryMutationError("Create or select a workspace before adding repositories.");
      return;
    }

    const repositoryUrl = newRepositoryUrl.trim();
    if (!repositoryUrl) {
      setRepositoryMutationError("Repository URL is required.");
      return;
    }
    if (!/^https?:\/\//.test(repositoryUrl)) {
      setRepositoryMutationError("Repository URL must start with http:// or https://.");
      return;
    }

    logRepositoryCreateStart(activeWorkspaceId, repositoryUrl);
    void createRepositoryMutation
      .mutateAsync({ workspaceId: activeWorkspaceId, repositoryUrl })
      .then((response) => {
        logRepositoryCreateSuccess(activeWorkspaceId, repositoryUrl, response.repository?.repositoryId ?? "");
        setRepositoryMutationError("");
        setNewRepositoryUrl("");
      })
      .catch((error) => {
        const errorMessage = formatMutationError(error);
        logRepositoryCreateFailure(activeWorkspaceId, repositoryUrl, errorMessage);
        setRepositoryMutationError(`Failed to add repository: ${errorMessage}`);
      });
  }

  function handleUpdateRepository(repositoryId: string) {
    if (!hasActiveWorkspace) {
      setRepositoryMutationError("Create or select a workspace before editing repositories.");
      return;
    }

    const repositoryUrl = (repositoryEdits[repositoryId] ?? "").trim();
    if (!repositoryUrl) return;

    void updateRepositoryMutation
      .mutateAsync({
        workspaceId: activeWorkspaceId,
        repositoryId,
        repositoryUrl,
      })
      .then(() => {
        setRepositoryMutationError("");
      })
      .catch((error) => {
        setRepositoryMutationError(`Failed to update repository: ${formatMutationError(error)}`);
      });
  }

  function handleDeleteRepository(repositoryId: string) {
    if (!hasActiveWorkspace) {
      setRepositoryMutationError("Create or select a workspace before deleting repositories.");
      return;
    }

    void deleteRepositoryMutation
      .mutateAsync({
        workspaceId: activeWorkspaceId,
        repositoryId,
      })
      .then(() => {
        setRepositoryMutationError("");
      })
      .catch((error) => {
        setRepositoryMutationError(`Failed to delete repository: ${formatMutationError(error)}`);
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
          {!hasActiveWorkspace && (
            <div
              style={{ marginBottom: "var(--space-2)", color: "var(--color-text-tertiary)", fontSize: "var(--font-size-sm)" }}
              data-testid="repository-workspace-hint"
            >
              Create a workspace from the sidebar first, then add repositories.
            </div>
          )}
          {repositoryMutationError && (
            <div
              style={{ marginBottom: "var(--space-2)", color: "var(--color-status-failed)", fontSize: "var(--font-size-sm)" }}
              data-testid="repository-mutation-error"
              role="alert"
            >
              {repositoryMutationError}
            </div>
          )}
          <div style={{ display: "grid", gridTemplateColumns: "1fr auto", gap: "var(--space-2)", maxWidth: 700 }}>
            <input
              style={inputStyle}
              type="text"
              value={newRepositoryUrl}
              onChange={(e) => {
                setNewRepositoryUrl(e.target.value);
                if (repositoryMutationError) {
                  setRepositoryMutationError("");
                }
              }}
              placeholder="https://github.com/org/repo"
              data-testid="create-repository-url"
              disabled={!hasActiveWorkspace}
            />
            <button
              style={primaryButtonStyle}
              onClick={handleCreateRepository}
              disabled={!hasActiveWorkspace || createRepositoryMutation.isPending}
            >
              {createRepositoryMutation.isPending ? "Adding..." : "Add Repository"}
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
                  disabled={!hasActiveWorkspace}
                />
                <button
                  style={secondaryButtonStyle}
                  onClick={() => handleUpdateRepository(repository.repositoryId)}
                  disabled={!hasActiveWorkspace}
                >
                  Save
                </button>
                <button
                  style={dangerButtonStyle}
                  onClick={() => handleDeleteRepository(repository.repositoryId)}
                  disabled={!hasActiveWorkspace}
                >
                  Delete
                </button>
              </div>
            ))}
            {repositories.length === 0 && (
              <div style={{ color: "var(--color-text-tertiary)", fontSize: "var(--font-size-sm)" }}>No repositories yet.</div>
            )}
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
