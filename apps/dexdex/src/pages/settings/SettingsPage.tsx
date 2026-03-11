import { dexdexPageDefinitions } from "../../contracts/dexdex-page";
import type { DexDexPageId } from "../../contracts/dexdex-page";
import type { DesktopLocalStoreState } from "../../lib/desktop-local-store";
import type { UpdateLocalStore } from "../../App";

type SettingsPageProps = {
  localStoreState: DesktopLocalStoreState;
  updateLocalStore: UpdateLocalStore;
};

export function SettingsPage({ localStoreState, updateLocalStore }: SettingsPageProps) {
  return (
    <div className="content-body">
      <section className="panel">
        <header className="panel-header">Preferences</header>
        <div className="panel-body">
          <div className="settings-row">
            <div>
              <p className="settings-row-label">Default Page</p>
              <p className="settings-row-description">Which page opens after connecting to a workspace.</p>
            </div>
            <select
              className="form-select settings-select"
              value={localStoreState.settings.defaultPage}
              onChange={(event) =>
                updateLocalStore((current) => ({
                  ...current,
                  settings: {
                    ...current.settings,
                    defaultPage: event.target.value as DexDexPageId,
                  },
                }))
              }
            >
              {dexdexPageDefinitions.map((page) => (
                <option key={page.id} value={page.id}>{page.label}</option>
              ))}
            </select>
          </div>

          <div className="settings-row">
            <div>
              <p className="settings-row-label">Compact Mode</p>
              <p className="settings-row-description">Reduce spacing and typography scale.</p>
            </div>
            <input
              type="checkbox"
              checked={localStoreState.settings.compactMode}
              onChange={(event) =>
                updateLocalStore((current) => ({
                  ...current,
                  settings: { ...current.settings, compactMode: event.target.checked },
                }))
              }
              className="settings-checkbox"
            />
          </div>

          <div className="settings-row">
            <div>
              <p className="settings-row-label">Auto Start Stream</p>
              <p className="settings-row-description">Start live stream automatically on Worktrees page.</p>
            </div>
            <input
              type="checkbox"
              checked={localStoreState.settings.autoStartStream}
              onChange={(event) =>
                updateLocalStore((current) => ({
                  ...current,
                  settings: { ...current.settings, autoStartStream: event.target.checked },
                }))
              }
              className="settings-checkbox"
            />
          </div>
        </div>
      </section>
    </div>
  );
}
