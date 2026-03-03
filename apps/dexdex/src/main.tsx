import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { ConnectQueryProvider } from "./lib/connect-query-provider";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ConnectQueryProvider>
      <App />
    </ConnectQueryProvider>
  </StrictMode>,
);
