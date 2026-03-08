import { type FormEvent } from "react";
import type { SavedWorkspaceProfile } from "../contracts/workspace-profile";
import { WorkspaceMode } from "../contracts/workspace-mode";

export type WorkspacePickerProps = {
  status: "idle" | "resolving" | "resolved" | "error";
  errorMessage: string | null;
  pickerMessage: string | null;
  mode: WorkspaceMode;
  workspaceIdInput: string;
  remoteEndpointUrl: string;
  remoteToken: string;
  savedProfiles: SavedWorkspaceProfile[];
  onModeChange: (mode: WorkspaceMode) => void;
  onWorkspaceIdChange: (value: string) => void;
  onRemoteEndpointChange: (value: string) => void;
  onRemoteTokenChange: (value: string) => void;
  onOpenWorkspace: (event: FormEvent<HTMLFormElement>) => void;
  onSaveProfile: () => void;
  onEditProfile: (profile: SavedWorkspaceProfile) => void;
  onDeleteProfile: (profile: SavedWorkspaceProfile) => void;
};

const modeDefinitions: ReadonlyArray<{ value: WorkspaceMode; label: string }> = [
  { value: WorkspaceMode.Local, label: "LOCAL" },
  { value: WorkspaceMode.Remote, label: "REMOTE" },
];

export function WorkspacePicker({
  status,
  errorMessage,
  pickerMessage,
  mode,
  workspaceIdInput,
  remoteEndpointUrl,
  remoteToken,
  savedProfiles,
  onModeChange,
  onWorkspaceIdChange,
  onRemoteEndpointChange,
  onRemoteTokenChange,
  onOpenWorkspace,
  onSaveProfile,
  onEditProfile,
  onDeleteProfile,
}: WorkspacePickerProps) {
  const isRemoteMode = mode === WorkspaceMode.Remote;

  return (
    <main className="picker-shell">
      <div className="picker-container">
        <div className="picker-header">
          <div className="picker-logo">DexDex</div>
          <p className="picker-subtitle">Select a workspace to get started.</p>
        </div>

        {savedProfiles.length > 0 ? (
          <section className="panel picker-panel">
            <header className="panel-header">Recent Workspaces</header>
            <div className="panel-body">
              {savedProfiles.map((profile) => (
                <article
                  key={profile.workspaceId}
                  className="picker-profile"
                  onClick={() => onEditProfile(profile)}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      onEditProfile(profile);
                    }
                  }}
                >
                  <div>
                    <p className="picker-profile-name">{profile.workspaceId}</p>
                    <p className="picker-profile-meta">
                      {profile.mode} · {profile.remoteEndpointUrl ?? "managed-local"}
                    </p>
                  </div>
                  <div className="picker-profile-actions" onClick={(event) => event.stopPropagation()}>
                    <button type="button" className="btn btn-ghost btn-sm" onClick={() => onDeleteProfile(profile)}>
                      Remove
                    </button>
                  </div>
                </article>
              ))}
            </div>
          </section>
        ) : null}

        <section className="panel picker-panel">
          <header className="panel-header">Open Workspace</header>
          <div className="panel-body">
            <form onSubmit={onOpenWorkspace} className="form-stack">
              <div className="form-group">
                <label className="form-label" htmlFor="ws-id">Workspace ID</label>
                <input
                  id="ws-id"
                  className="form-input"
                  value={workspaceIdInput}
                  onChange={(event) => onWorkspaceIdChange(event.target.value)}
                  placeholder="workspace-1"
                />
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="ws-mode">Mode</label>
                <select
                  id="ws-mode"
                  className="form-select"
                  value={mode}
                  onChange={(event) => onModeChange(event.target.value as WorkspaceMode)}
                >
                  {modeDefinitions.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                </select>
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="ws-endpoint">Remote Endpoint</label>
                <input
                  id="ws-endpoint"
                  className="form-input"
                  type="url"
                  value={remoteEndpointUrl}
                  disabled={!isRemoteMode}
                  onChange={(event) => onRemoteEndpointChange(event.target.value)}
                  placeholder="https://dexdex.example/rpc"
                />
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="ws-token">Token (not persisted)</label>
                <input
                  id="ws-token"
                  className="form-input"
                  type="password"
                  value={remoteToken}
                  disabled={!isRemoteMode}
                  onChange={(event) => onRemoteTokenChange(event.target.value)}
                />
              </div>

              <div className="form-actions">
                <button type="submit" className="btn btn-primary" disabled={status === "resolving"}>
                  {status === "resolving" ? "Connecting..." : "Connect"}
                </button>
                <button type="button" className="btn btn-secondary" onClick={onSaveProfile}>
                  Save Profile
                </button>
              </div>
            </form>

            {pickerMessage ? <p className="picker-message">{pickerMessage}</p> : null}
            {errorMessage ? <p className="picker-error">{errorMessage}</p> : null}
          </div>
        </section>
      </div>
    </main>
  );
}
