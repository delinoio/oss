// SPDX-License-Identifier: Apache-2.0
// Isolated synthetic browser entry; no native/account/business adapters exist.
import { createRoot } from "react-dom/client";
import { useRef, useState } from "react";
import { Disclosure, DisclosureButton, DisclosureContent, DisclosureDensity, DisclosureSummary } from "./disclosure";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("zoom") === "2") document.body.style.zoom = "2";
function Fixture() {
  const [nativeOpen, setNativeOpen] = useState(true), [buttonOpen, setButtonOpen] = useState(true);
  const native = useRef<HTMLDetailsElement>(null);
  const label = args.get("language") === "ko" ? "전체 관찰 내용을 표시하는 긴 제목" : "A long complete observation title that wraps";
  return <main style={{ width: "100%", padding: 8, boxSizing: "border-box" }}>
    <button data-owner-close onClick={() => setNativeOpen(false)}>Owner close</button>
    <button data-owner-open onClick={() => setNativeOpen(true)}>Owner open</button>
    <Disclosure ref={native} open={nativeOpen} density={DisclosureDensity.Settings}><DisclosureSummary>{label}</DisclosureSummary><input data-native-field aria-label="Native fixture field" defaultValue="unsent native draft" /><Disclosure><DisclosureSummary>Nested details</DisclosureSummary><p>Exact inert fixture evidence.</p></Disclosure></Disclosure>
    <DisclosureButton density={DisclosureDensity.Compact} aria-expanded={buttonOpen} aria-controls="controlled-content" onClick={() => setButtonOpen(!buttonOpen)}>Compact observations</DisclosureButton><DisclosureContent id="controlled-content" hidden={!buttonOpen}><input data-controlled-field aria-label="Controlled fixture field" defaultValue="unsent controlled draft" /></DisclosureContent>
  </main>;
}
createRoot(document.getElementById("root")!).render(<Fixture />);
