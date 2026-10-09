// SPDX-License-Identifier: Apache-2.0
// Synthetic permission presentation; grants no native/account authority.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { AgentPermissions, Harness } from "./configuration-fields";
import { ScrollPicker } from "./scroll-picker";
import { i18n } from "./localization";
import type { Document } from "./documents";
import "./themes.css";
import "./styles.css";
import "./settings-presentation.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
const harness = Object.values(Harness).find(value => value === args.get("harness")) ?? Harness.Codex;
const query = { loaded: true, nextPageToken: "", append: () => {}, retry: () => {}, reload: () => {} };
function Fixture() {
 const [options, setOptions] = useState<Document>({ permission: "default", ...(harness === Harness.Claude ? { claude_permission: args.get("unsupported") === "true" ? "future-mode" : "default" } : args.get("unsupported") === "true" ? { permission: "future-mode" } : {}), future_option: "keep" });
 const [active, setActive] = useState(true), [pending, setPending] = useState(false), [source, setSource] = useState("source");
 return <main className="settings-content" data-fixture="__agentPermissionsFixture"><div className="settings-content-column"><div className="settings-task-body"><form className="agent-configuration" onSubmit={event => event.preventDefault()}><fieldset disabled={pending}>
 <label>Name<input defaultValue={args.get("editing") === "true" ? "Original Worker" : "New Worker"}/></label>
 <ScrollPicker label="Account source" value={source} change={setSource} options={[{ id: "source", label: "Synthetic account source" }]} query={query} active={active} disabled={pending}/>
 <div className="agent-permissions"><AgentPermissions harness={harness} options={options} change={setOptions} active={active} disabled={pending}/></div>
 </fieldset></form><div className="actions"><button onClick={() => setPending(true)}>Fixture pending</button><button onClick={() => setActive(false)}>Fixture inactive</button></div><output data-options={JSON.stringify(options)}>{JSON.stringify(options)}</output></div></div></main>;
}
createRoot(document.getElementById("root")!).render(<Fixture/>);
