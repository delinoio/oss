// SPDX-License-Identifier: Apache-2.0
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ApiProvider } from "./api";
import { App } from "./app";
import "./styles.css";
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ApiProvider>
      <App />
    </ApiProvider>
  </StrictMode>,
);
