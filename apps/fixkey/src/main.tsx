import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App, CandidatePicker } from "./App";
import "./style.css";

const root = document.createElement("div");
document.body.append(root);
const params = new URLSearchParams(window.location.search);
createRoot(root).render(
  params.get("window") === "candidates" ? (
    <CandidatePicker language={params.get("language") === "ko" ? "ko" : "en"} />
  ) : (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <App />
    </QueryClientProvider>
  ),
);
