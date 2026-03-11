import { type FormEvent, useState } from "react";
import type { DesktopLocalStoreState } from "../../lib/desktop-local-store";
import type { UpdateLocalStore } from "../../App";

type AutomationsPageProps = {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
};

export function AutomationsPage({ localStoreState, updateLocalStore }: AutomationsPageProps) {
  const [newName, setNewName] = useState("");
  const [newSchedule, setNewSchedule] = useState("Every weekday 09:00");

  function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = newName.trim();
    if (name.length === 0) return;

    updateLocalStore((current) => {
      const id = `automation-${Date.now().toString()}`;
      return {
        ...current,
        automations: [
          ...current.automations,
          { id, name, schedule: newSchedule.trim() || "Manual", enabled: true, lastRunAt: null },
        ],
        lastSelectedAutomationId: id,
      };
    });
    setNewName("");
  }

  return (
    <div className="content-body">
      <div className="dashboard-grid two-columns">
        <section className="panel">
          <header className="panel-header">Automation Queue</header>
          <div className="panel-body">
            {localStoreState.automations.length === 0 ? (
              <p className="empty-state">No automations configured.</p>
            ) : (
              <div className="stack-gap">
                {localStoreState.automations.map((automation) => (
                  <article key={automation.id} className="automation-item">
                    <div className="automation-item-header">
                      <div>
                        <p className="automation-item-name">{automation.name}</p>
                        <p className="automation-item-schedule">{automation.schedule}</p>
                      </div>
                      {!automation.enabled ? (
                        <span className="badge badge-muted">Disabled</span>
                      ) : null}
                    </div>
                    <div className="automation-item-actions">
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() =>
                          updateLocalStore((current) => ({
                            ...current,
                            automations: current.automations.map((item) =>
                              item.id === automation.id
                                ? { ...item, enabled: !item.enabled }
                                : item,
                            ),
                          }))
                        }
                      >
                        {automation.enabled ? "Disable" : "Enable"}
                      </button>
                      <button
                        type="button"
                        className="btn btn-danger btn-sm"
                        onClick={() =>
                          updateLocalStore((current) => ({
                            ...current,
                            automations: current.automations.filter((item) => item.id !== automation.id),
                            lastSelectedAutomationId:
                              current.lastSelectedAutomationId === automation.id
                                ? null
                                : current.lastSelectedAutomationId,
                          }))
                        }
                      >
                        Delete
                      </button>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </div>
        </section>

        <section className="panel">
          <header className="panel-header">Create Automation</header>
          <div className="panel-body">
            <form onSubmit={handleCreate} className="form-stack">
              <div className="form-group">
                <label className="form-label" htmlFor="auto-name">Name</label>
                <input
                  id="auto-name"
                  className="form-input"
                  value={newName}
                  onChange={(event) => setNewName(event.target.value)}
                  placeholder="Nightly Stream Health"
                />
              </div>
              <div className="form-group">
                <label className="form-label" htmlFor="auto-schedule">Schedule</label>
                <input
                  id="auto-schedule"
                  className="form-input"
                  value={newSchedule}
                  onChange={(event) => setNewSchedule(event.target.value)}
                  placeholder="Every weekday 09:00"
                />
              </div>
              <div className="form-actions">
                <button type="submit" className="btn btn-primary btn-sm">Create</button>
              </div>
            </form>
          </div>
        </section>
      </div>
    </div>
  );
}
