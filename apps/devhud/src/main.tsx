import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";
import { detectApplicationPlatform } from "./runtime/platform";
import "./styles.css";

const root = document.getElementById("root");

if (root === null) {
  throw new Error("DevHud root element is missing");
}

createRoot(root).render(
  <StrictMode>
    <App platform={detectApplicationPlatform(navigator.userAgent)} />
  </StrictMode>,
);
