import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import App from "./App";
import { queryClient } from "./lib/query-client";
import { createDefaultTransport } from "./lib/transport";

const transport = createDefaultTransport();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <App />
      </TransportProvider>
    </QueryClientProvider>
  </StrictMode>,
);
