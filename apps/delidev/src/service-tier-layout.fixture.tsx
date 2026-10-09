// SPDX-License-Identifier: Apache-2.0
// Synthetic configuration geometry; excluded from the production entry point.
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { ServiceTierField } from "./configuration-fields";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en");
document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
function Fixture() {
  const [value, setValue] = useState<string | undefined>();
  return <main style={{ padding: 16, maxWidth: 640, margin: "auto" }}><form onSubmit={event => event.preventDefault()}><ServiceTierField value={value} unavailable={false} change={setValue} clear={() => setValue(undefined)} /><output>{value ?? "omitted"}</output></form></main>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
