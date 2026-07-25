import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";
import { tauriPersistenceAdapter } from "./persistence/tauri";
import "./styles.css";

const root = document.getElementById("root");

if (root === null) {
  throw new Error("DevHud root element is missing");
}

createRoot(root).render(
  <StrictMode>
    <App storage={tauriPersistenceAdapter} />
  </StrictMode>,
);
