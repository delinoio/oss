import { invoke } from "@tauri-apps/api/core";

import { createTauriPersistenceAdapter, type TauriPersistenceBridge } from "./storage";

interface NativeCommandResults {
  read_settings: string | null;
  reset_dev_hud: void;
  write_settings: void;
  read_widget_configuration: string | null;
  write_widget_configuration: void;
}

export const tauriPersistenceBridge: TauriPersistenceBridge = {
  readSettings: () => invoke<NativeCommandResults["read_settings"]>("read_settings"),
  resetDevHud: () => invoke<NativeCommandResults["reset_dev_hud"]>("reset_dev_hud"),
  writeSettings: (record) =>
    invoke<NativeCommandResults["write_settings"]>("write_settings", { record }),
  readWidgetConfiguration: () =>
    invoke<NativeCommandResults["read_widget_configuration"]>(
      "read_widget_configuration",
    ),
  writeWidgetConfiguration: (record) =>
    invoke<NativeCommandResults["write_widget_configuration"]>(
      "write_widget_configuration",
      { record },
    ),
};

export const tauriPersistenceAdapter = createTauriPersistenceAdapter(tauriPersistenceBridge);
