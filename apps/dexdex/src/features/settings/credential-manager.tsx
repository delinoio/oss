/**
 * Credential management component for remote workspace connections.
 * Uses Tauri IPC for secure credential storage.
 */

import { type CSSProperties, type FormEvent, useCallback, useEffect, useState } from "react";

interface CredentialListEntry {
  name: string;
  credential_type: string;
}

async function invokeCommand<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  if (typeof window !== "undefined" && "__TAURI__" in window) {
    const { invoke } = await import("@tauri-apps/api/core");
    return invoke<T>(cmd, args);
  }
  throw new Error("Tauri runtime not available");
}

export function CredentialManager() {
  const [credentials, setCredentials] = useState<CredentialListEntry[]>([]);
  const [showAdd, setShowAdd] = useState(false);
  const [name, setName] = useState("");
  const [credType, setCredType] = useState("github_token");
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);

  const loadCredentials = useCallback(async () => {
    try {
      const creds = await invokeCommand<CredentialListEntry[]>("list_credentials");
      setCredentials(creds);
      setError(null);
    } catch (e) {
      setError("Failed to load credentials. Tauri runtime may not be available.");
    }
  }, []);

  useEffect(() => {
    loadCredentials();
  }, [loadCredentials]);

  async function handleAdd(e: FormEvent) {
    e.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName || !value.trim()) return;

    try {
      await invokeCommand("store_credential", {
        name: trimmedName,
        credentialType: credType,
        value: value.trim(),
      });
      setName("");
      setValue("");
      setShowAdd(false);
      await loadCredentials();
    } catch (err) {
      setError(`Failed to store credential: ${err}`);
    }
  }

  async function handleDelete(credName: string) {
    try {
      await invokeCommand("delete_credential", { name: credName });
      await loadCredentials();
    } catch (err) {
      setError(`Failed to delete credential: ${err}`);
    }
  }

  const labelStyle: CSSProperties = {
    display: "block",
    fontSize: "var(--font-size-sm)",
    fontWeight: 500,
    marginBottom: "var(--space-1)",
    color: "var(--color-text-secondary)",
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

  return (
    <div data-testid="credential-manager">
      {error && (
        <div
          style={{
            padding: "var(--space-2) var(--space-3)",
            marginBottom: "var(--space-3)",
            backgroundColor: "var(--color-bg-tertiary)",
            borderRadius: "var(--radius-md)",
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-secondary)",
          }}
        >
          {error}
        </div>
      )}

      {credentials.length === 0 && !showAdd && (
        <div
          style={{
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-tertiary)",
            marginBottom: "var(--space-3)",
          }}
        >
          No credentials configured.
        </div>
      )}

      {credentials.map((cred) => (
        <div
          key={cred.name}
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "var(--space-2) var(--space-3)",
            marginBottom: "var(--space-2)",
            backgroundColor: "var(--color-bg-secondary)",
            borderRadius: "var(--radius-md)",
            border: "1px solid var(--color-border)",
          }}
        >
          <div>
            <div style={{ fontSize: "var(--font-size-sm)", fontWeight: 500, color: "var(--color-text-primary)" }}>
              {cred.name}
            </div>
            <div style={{ fontSize: "var(--font-size-xs)", color: "var(--color-text-tertiary)" }}>
              {cred.credential_type}
            </div>
          </div>
          <button
            onClick={() => handleDelete(cred.name)}
            style={{
              padding: "var(--space-1) var(--space-2)",
              borderRadius: "var(--radius-sm)",
              fontSize: "var(--font-size-xs)",
              color: "var(--color-text-secondary)",
              border: "1px solid var(--color-border)",
              cursor: "pointer",
            }}
          >
            Remove
          </button>
        </div>
      ))}

      {showAdd ? (
        <form onSubmit={handleAdd} style={{ marginTop: "var(--space-3)" }}>
          <div style={{ marginBottom: "var(--space-2)" }}>
            <label htmlFor="cred-name" style={labelStyle}>Name</label>
            <input
              id="cred-name"
              style={inputStyle}
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g., GitHub Token"
              autoFocus
            />
          </div>
          <div style={{ marginBottom: "var(--space-2)" }}>
            <label htmlFor="cred-type" style={labelStyle}>Type</label>
            <select
              id="cred-type"
              style={{ ...inputStyle, cursor: "pointer" }}
              value={credType}
              onChange={(e) => setCredType(e.target.value)}
            >
              <option value="github_token">GitHub Token</option>
              <option value="api_key">API Key</option>
              <option value="workspace_token">Workspace Token</option>
            </select>
          </div>
          <div style={{ marginBottom: "var(--space-3)" }}>
            <label htmlFor="cred-value" style={labelStyle}>Value</label>
            <input
              id="cred-value"
              style={inputStyle}
              type="password"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="Token or key value..."
            />
          </div>
          <div style={{ display: "flex", gap: "var(--space-2)" }}>
            <button
              type="submit"
              disabled={!name.trim() || !value.trim()}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                fontWeight: 500,
                backgroundColor: name.trim() && value.trim() ? "var(--color-accent)" : "var(--color-bg-tertiary)",
                color: name.trim() && value.trim() ? "var(--color-text-inverse)" : "var(--color-text-tertiary)",
                cursor: name.trim() && value.trim() ? "pointer" : "not-allowed",
              }}
            >
              Save
            </button>
            <button
              type="button"
              onClick={() => { setShowAdd(false); setName(""); setValue(""); }}
              style={{
                padding: "var(--space-2) var(--space-4)",
                borderRadius: "var(--radius-md)",
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-secondary)",
                border: "1px solid var(--color-border)",
                cursor: "pointer",
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <button
          onClick={() => setShowAdd(true)}
          style={{
            marginTop: "var(--space-2)",
            padding: "var(--space-2) var(--space-4)",
            borderRadius: "var(--radius-md)",
            fontSize: "var(--font-size-sm)",
            fontWeight: 500,
            color: "var(--color-text-secondary)",
            border: "1px solid var(--color-border)",
            cursor: "pointer",
          }}
        >
          Add Credential
        </button>
      )}
    </div>
  );
}
