import { createRoot } from "react-dom/client";
import { LanguageProvider } from "./language";
import { Desktop } from "./desktop";
import { ShortcutPreferenceProvider } from "./shortcut-preference-controller";
import { SidebarProvider } from "./sidebar-preference";
import { DateFormatProvider } from "./date-format";
import { AppearanceProvider } from "./appearance";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";

createRoot(document.getElementById("root")!).render(<LanguageProvider><AppearanceProvider><SidebarProvider><DateFormatProvider><ShortcutPreferenceProvider><Desktop /></ShortcutPreferenceProvider></DateFormatProvider></SidebarProvider></AppearanceProvider></LanguageProvider>);
