// SPDX-License-Identifier: Apache-2.0
// Synthetic browser presentation only; not a production or native entry point.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { ConnectionsPage, type ConnectionPageSlots, PersistentConnectionView } from "./connections-page";
import { SavedConnections, SavedConnectionState, type SavedConnectionActions } from "./saved-connections";
import { copy, i18n } from "./localization";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en");
document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
document.documentElement.dataset.connectionsFixture = "__connectionsFixture";
const count = () => { document.documentElement.dataset.actions = String(Number(document.documentElement.dataset.actions ?? "0") + 1); };
const profiles = ["Studio server", "Build server"].map((name, index) => ({ version: 1, revision: 1, id: `profile-${index}`, name, endpoint: `https://${index ? "build" : "studio"}.example/${"long-path/".repeat(8)}`, server_id: `server-${index}`, pairing_id: `pair-${index}`, device_id: `device-${index}`, state: SavedConnectionState.Paired, created_at: "2026-10-08T00:00:00Z" }));
const actions: SavedConnectionActions = { list: async () => profiles, removed: async () => ({ connections: [] }), open: async () => count(), remove: async () => { count(); throw "fixture"; }, rename: async () => { count(); throw "fixture"; }, pair: async () => { count(); throw "fixture"; }, retry: async () => { count(); throw "fixture"; }, retainedWorker: async () => { count(); throw "fixture"; } };
function Fixture() {
  const [slots, setSlots] = useState<ConnectionPageSlots>();
  return <main className="settings-content" aria-label="Connections fixture"><div className="settings-content-column"><div className="settings-category-heading"><div className="settings-category-title"><h1>{copy("settings.connectionDiagnostics_b30b0d")}</h1><p>{copy("settings.connectionDescription")}</p></div></div><ConnectionsPage local onSlots={setSlots} />
    <PersistentConnectionView target={slots?.current} hidden><header className="connections-local-heading"><div><h3>{copy("settings.connections.thisComputer")}</h3><p>{copy("local-server.localServer_caf011")}</p><p>{copy("settings.connections.connected")}</p></div><button onClick={count}>{copy("local-server.stopLocalServer_c1eeb5")}</button></header><p className="connections-address">{copy("settings.connections.address")}: http://127.0.0.1:46300</p><p>{copy("local-server.stoppingTheServerDisconnectsAllClients_c28c49")}</p></PersistentConnectionView>
    <SavedConnections target={slots?.saved} advancedTarget={slots?.advanced} visible={Boolean(slots)} close={() => {}} actions={actions} />
    <PersistentConnectionView target={slots?.advanced} hidden><p role="alert">Retained update needs inspection</p><button onClick={count}>{copy("local-registration.checkDesktopRegistration_a540c5")}</button></PersistentConnectionView>
  </div></main>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
