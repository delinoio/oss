import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { queryClient } from "./lib/query-client";
import { createDefaultTransport } from "./lib/transport";
import { App } from "./app";
import "./styles/globals.css";

const transport = createDefaultTransport();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </TransportProvider>
    </QueryClientProvider>
  </StrictMode>,
);
