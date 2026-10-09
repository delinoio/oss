// SPDX-License-Identifier: Apache-2.0
// Dedicated auxiliary entry: no Desktop, transport, product queries or device
// preference controllers are mounted here.
import { createRoot } from "react-dom/client";
import { TrayStatus, trayPanelBridge } from "./tray-status";
import "./themes.css";
const instance = new URLSearchParams(window.location.search).get("instance") ?? "";
createRoot(document.getElementById("root")!).render(<TrayStatus instance={instance} bridge={trayPanelBridge(instance)} />);
