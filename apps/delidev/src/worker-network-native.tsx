import { copy, useLocale } from "./localization";
import { createContext, useContext, useRef, useState, type ReactNode } from "react";
import type { PairingAuthority } from "./pairing-grant";
import { useSettingsOpening } from "./settings-lifetime";
import { encryptedInput, workerRecipient, type WorkerRecipient } from "./worker-network";
export enum WorkerNetworkAction { Prepare = "prepare", Import = "import", Status = "status" }
export type ControlWorkerNetwork = (machine: string, action: WorkerNetworkAction, ciphertext: Uint8Array, digest: string) => Promise<unknown>;
const Context = createContext<ControlWorkerNetwork | undefined>(undefined);
export function WorkerNetworkControlProvider({ control, children }: { control: ControlWorkerNetwork; children: ReactNode }) {
  useLocale(); return <Context.Provider value={control}>{children}</Context.Provider>; }
export function WorkerNetworkNative({ machine, authority, prepared }: { machine: string; authority: PairingAuthority; prepared: (recipient: WorkerRecipient) => void }) {
  useLocale();
  const control = useContext(Context), opening = useSettingsOpening();
  const [busy, setBusy] = useState(false), [problem, setProblem] = useState(""), [outcome, setOutcome] = useState("");
  const [ciphertext, setCiphertext] = useState(""), [digest, setDigest] = useState(""), [confirm, setConfirm] = useState(false);
  const pending = useRef(false);
  const run = async (action: WorkerNetworkAction) => {
    if (!control || pending.current || opening?.disposed) return;
    const input = action === WorkerNetworkAction.Import ? encryptedInput(ciphertext) : new Uint8Array();
    if (!input || action === WorkerNetworkAction.Import && (!confirm || !/^[0-9a-f]{64}$/.test(digest))) { setProblem("Paste the encrypted Base64 bundle and separately authenticated digest, then confirm the original recipient import."); return; }
    pending.current = true; setBusy(true); setProblem(""); setOutcome("");
    try {
      const result = opening ? await opening.native(() => control(machine, action, input, action === WorkerNetworkAction.Import ? digest : "")) : await control(machine, action, input, action === WorkerNetworkAction.Import ? digest : "");
      if (opening?.disposed) return;
      if (action !== WorkerNetworkAction.Import) { const recipient = workerRecipient(result, authority, machine); if (!recipient) throw new Error("recipient"); prepared(recipient); setOutcome("Original public recipient inspected. Its protected key stays on this computer."); }
      else { const row = result as Record<string, unknown>; if (row.version !== 1 || row.ciphertext_digest !== digest || typeof row.generation !== "string" || !/^[1-9][0-9]{0,18}$/.test(row.generation)) throw new Error("import"); setOutcome(`Encrypted generation ${row.generation} imported. Inspect current control status after an explicit Worker restart. Import alone does not prove network use.`); setCiphertext(""); setDigest(""); setConfirm(false); }
    } catch { if (!opening?.disposed) setProblem("The original same-computer Worker operation could not be verified. Inspect its registration and current route. An import may have committed; retain the original encrypted input and digest for explicit reconciliation."); }
    finally { pending.current = false; if (!opening?.disposed) setBusy(false); }
  };
  return <section aria-label={copy("worker-network-native.thisComputerSWorkerNetwork_ddbb96")}><h4>{copy("worker-network-native.thisComputerSMatchingWorker_0db01d")}</h4><p>{copy("worker-network-native.theseControlsUseOnlyTheAlready_4de670")}</p>
    {!control ? <p>{copy("worker-network-native.openTheTrustedDesktopAppTo_dce615")}</p> : <>
      <button disabled={busy} onClick={() => void run(WorkerNetworkAction.Prepare)}>{copy("worker-network-native.prepareProtectedRecipient_5b5127")}</button><button disabled={busy} onClick={() => void run(WorkerNetworkAction.Status)}>{copy("worker-network-native.inspectOriginalPublicRecipient_579cd7")}</button>
      <label>{copy("worker-network-native.encryptedBundleToImportBase64_db23d6")}<textarea rows={4} maxLength={131072} spellCheck={false} disabled={busy} value={ciphertext} onChange={event => { setCiphertext(event.target.value); setConfirm(false); }} /></label><label>{copy("worker-network-native.separatelyAuthenticatedDigest_be070a")}<input maxLength={64} disabled={busy} value={digest} onChange={event => { setDigest(event.target.value); setConfirm(false); }} /></label><label><input type="checkbox" disabled={busy} checked={confirm} onChange={event => setConfirm(event.target.checked)} />{copy("worker-network-native.iVerifiedThisDigestIndependentlyAnd_638722")}</label><button disabled={busy || !confirm} onClick={() => void run(WorkerNetworkAction.Import)}>{copy("worker-network-native.importConfirmedEncryptedConfiguration_30d15e")}</button>
    </>}{outcome ? <p role="status">{outcome}</p> : null}{problem ? <p role="alert">{problem}</p> : null}
  </section>;
}
