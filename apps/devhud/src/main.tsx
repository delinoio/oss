import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";
import { selectSupportedLanguage } from "./localization";
import "./styles.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("DevHUD root element is missing");
}

const language = selectSupportedLanguage(navigator.languages);
document.documentElement.lang = language;

createRoot(root).render(
  <StrictMode>
    <App language={language} />
  </StrictMode>,
);
