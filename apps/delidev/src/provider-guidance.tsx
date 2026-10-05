// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";

export enum ProviderGuidanceAction { Documentation = "documentation", ApiKeys = "api-keys" }
export function ProviderGuidance({ preset, documentation, keyCreation }: { preset?: string; documentation: string; keyCreation?: string }) {
  const [busy, setBusy] = useState(false), [status, setStatus] = useState("");
  const owner = useRef(0), pending = useRef(false);
  useEffect(() => { owner.current++; pending.current = false; setBusy(false); setStatus(""); return () => { owner.current++; }; }, [preset]);
  const open = async (action: ProviderGuidanceAction) => {
    if (!preset || pending.current || !isTauri()) return;
    const original = owner.current; pending.current = true; setBusy(true); setStatus("");
    try {
      // The native host selects its compiled official URL from this closed
      // identity/action. Server-provided help text cannot expand OS authority.
      await invoke("open_provider_guidance", { preset, action });
      if (owner.current === original) setStatus("Sent to your default browser. Create the key there, then connect it here.");
    } catch { if (owner.current === original) setStatus("Browser opening was not confirmed. Inspect your browser before trying again."); }
    finally { if (owner.current === original) { pending.current = false; setBusy(false); } }
  };
  return <div>
    {preset && isTauri() ? <div className="actions">
      {keyCreation ? <button type="button" disabled={busy} onClick={() => void open(ProviderGuidanceAction.ApiKeys)}>Open official key creation</button> : null}
      {documentation ? <button type="button" disabled={busy} onClick={() => void open(ProviderGuidanceAction.Documentation)}>Open provider documentation</button> : null}
    </div> : <>{keyCreation ? <p>Official key creation: <code>{keyCreation}</code></p> : null}{documentation ? <p>Provider documentation: <code>{documentation}</code></p> : null}</>}
    {status ? <p role="status">{status}</p> : null}
  </div>;
}
