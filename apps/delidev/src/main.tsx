import { createRoot } from "react-dom/client";
import { LanguageProvider } from "./language";
import { Desktop } from "./desktop";
import { AppearanceProvider } from "./appearance";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";

createRoot(document.getElementById("root")!).render(<LanguageProvider><AppearanceProvider><Desktop /></AppearanceProvider></LanguageProvider>);
