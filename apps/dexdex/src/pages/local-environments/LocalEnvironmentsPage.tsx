import { type FormEvent, useState } from "react";
import { LocalEnvironmentHealth, type DesktopLocalStoreState } from "../../lib/desktop-local-store";
import type { UpdateLocalStore } from "../../App";

type LocalEnvironmentsPageProps = {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
};

export function LocalEnvironmentsPage({ localStoreState, updateLocalStore }: LocalEnvironmentsPageProps) {
  const [envName, setEnvName] = useState("");
  const [envEndpoint, setEnvEndpoint] = useState("http://127.0.0.1:7878");

  function handleCreateEnvironment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = envName.trim();
    const endpointUrl = envEndpoint.trim();
    if (name.length === 0 || endpointUrl.length === 0) return;

    updateLocalStore((current) => {
      const id = `env-${Date.now().toString()}`;
      return {
        ...current,
        localEnvironments: [
          ...current.localEnvironments,
          {
            id,
            name,
            endpointUrl,
            health: LocalEnvironmentHealth.Unknown,
            lastCheckedAt: null,
            lastErrorMessage: null,
          },
        ],
        lastSelectedEnvironmentId: id,
      };
    });
    setEnvName("");
  }

  function runDiagnostics(environmentId: string) {
    updateLocalStore((current) => ({
      ...current,
      localEnvironments: current.localEnvironments.map((environment) => {
        if (environment.id !== environmentId) return environment;
        const reachable =
          environment.endpointUrl.startsWith("http://") ||
          environment.endpointUrl.startsWith("https://");
        return {
          ...environment,
          health: reachable ? LocalEnvironmentHealth.Healthy : LocalEnvironmentHealth.Unreachable,
          lastCheckedAt: new Date().toISOString(),
          lastErrorMessage: reachable ? null : "endpoint must use http/https",
        };
      }),
      lastSelectedEnvironmentId: environmentId,
    }));
  }

  return (
    <div className="content-body">
      <div className="dashboard-grid two-columns">
        <section className="panel">
          <header className="panel-header">Environment List</header>
          <div className="panel-body">
            {localStoreState.localEnvironments.length === 0 ? (
              <p className="empty-state">No local environments configured.</p>
            ) : (
              <div className="stack-gap">
                {localStoreState.localEnvironments.map((environment) => (
                  <article key={environment.id} className="env-item">
                    <p className="env-item-name">{environment.name}</p>
                    <p className="env-item-meta">{environment.endpointUrl}</p>
                    <p className="env-item-meta">
                      Health: {environment.health} · Last checked:{" "}
                      {environment.lastCheckedAt
                        ? new Date(environment.lastCheckedAt).toLocaleString()
                        : "never"}
                    </p>
                    <div className="env-item-actions">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => runDiagnostics(environment.id)}
                      >
                        Diagnostics
                      </button>
                      <button
                        type="button"
                        className="btn btn-danger btn-sm"
                        onClick={() =>
                          updateLocalStore((current) => ({
                            ...current,
                            localEnvironments: current.localEnvironments.filter(
                              (item) => item.id !== environment.id,
                            ),
                            lastSelectedEnvironmentId:
                              current.lastSelectedEnvironmentId === environment.id
                                ? null
                                : current.lastSelectedEnvironmentId,
                          }))
                        }
                      >
                        Remove
                      </button>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Add Environment</header>
          <div className="panel-body">
            <form onSubmit={handleCreateEnvironment} className="form-stack">
              <div className="form-group">
                <label className="form-label" htmlFor="env-name">Name</label>
                <input
                  id="env-name"
                  className="form-input"
                  value={envName}
                  onChange={(event) => setEnvName(event.target.value)}
                  placeholder="Staging Cluster"
                />
              </div>
              <div className="form-group">
                <label className="form-label" htmlFor="env-endpoint">Endpoint URL</label>
                <input
                  id="env-endpoint"
                  className="form-input"
                  value={envEndpoint}
                  onChange={(event) => setEnvEndpoint(event.target.value)}
                  placeholder="https://dexdex.example/rpc"
                />
              </div>
              <div className="form-actions">
                <button type="submit" className="btn btn-primary btn-sm">Add</button>
              </div>
            </form>
          </div>
        </section>
      </div>
    </div>
  );
}
