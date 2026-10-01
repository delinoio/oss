import { createRoot } from "react-dom/client";
import { Desktop } from "./desktop";
import { AppearanceProvider } from "./appearance";
import "./themes.css";
import "./styles.css";

createRoot(document.getElementById("root")!).render(<AppearanceProvider><Desktop /></AppearanceProvider>);
