// SPDX-License-Identifier: Apache-2.0
// Synthetic browser evidence. No native account, credential or mutation exists.
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useCallback, useRef, useState } from "react";
import { usePaginationChain } from "./scroll-pagination-query";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollPicker } from "./scroll-picker";
import { copy, i18n } from "./localization";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("zoom") === "2") document.body.style.zoom = "2";
function Fixture() {
  const root = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(true), [selected, setSelected] = useState("off-page");
  const reads = useRef<string[]>([]);
  const reader = useCallback(async (token: string) => {
    reads.current.push(token); await new Promise(resolve => setTimeout(resolve, 20));
    const index = Number(token || 0), row = { id: `row-${index}`, revision: 1n, text: `Fixture ${index} ${"wrapping content ".repeat(10)}` };
    return { rows: [{ id: row.id, revision: row.revision }], payload: [row], nextPageToken: index < 4 ? String(index + 1) : "" };
  }, []);
  const query = usePaginationChain("synthetic-layout", active, reader, false);
  const picker = { ...query, append: query.append };
  return <main style={{ padding: 8, width: "100%", maxWidth: 600, minWidth: 0, margin: "auto" }}>
    <button onClick={query.reload}>Fixture Load</button><button onClick={() => setActive(value => !value)}>Fixture visibility</button>
    <input aria-label="Fixture composer" placeholder="Keep connected focus" />
    <output data-fixture-count>{query.rows.length}:{query.payloadPages.length}:{reads.current.length}</output>
    <div ref={root} hidden={!active} style={{ overflow: "auto", height: 260, border: "1px solid var(--border)", marginTop: 8 }}>
      <ScrollPayloadWindow query={query} root={root} active={active} identity={row => row.id} revision={row => row.revision}>{payload => payload.map(row => <article key={row.id} style={{ minHeight: 190, overflowWrap: "anywhere", padding: 12 }}><p>{row.text}</p><button>Fixture action {row.id}</button></article>)}</ScrollPayloadWindow>
      <ScrollContinuation query={query} root={root} active={active} label={copy("schedules.savedSchedules_97f381")} />
    </div>
    <ScrollPicker label="Fixture picker" value={selected} selectedLabel="Retained off-page identity" change={setSelected} active options={query.rows.map(row => ({ id: row.id, label: "Duplicate label", disabled: row.id === "row-2" }))} query={picker} />
  </main>;
}
createRoot(document.getElementById("root")!).render(<QueryClientProvider client={new QueryClient()}><Fixture /></QueryClientProvider>);
