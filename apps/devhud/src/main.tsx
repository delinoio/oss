import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App, type AppProps } from "./App";
import { getLocalStorage, readPreferences, synchronizeDocumentPreferences } from "./shell";
import "./styles.css";

export function renderApp(props: AppProps = {}) {
  const root = document.getElementById("root");
  if (!root) throw new Error("DevHUD root element is missing");
  const preferences = readPreferences(getLocalStorage());
  synchronizeDocumentPreferences(document.documentElement, preferences, matchMedia("(prefers-color-scheme: dark)").matches, navigator.languages);
  createRoot(root).render(<StrictMode><App {...props} /></StrictMode>);
}
