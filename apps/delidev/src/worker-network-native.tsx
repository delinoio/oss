import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import {
  ownedMessage,
  useProductMessage,
  copy,
  useLocale,
} from "./localization";
import {
  createContext,
  useContext,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import { useSettingsTaskDismiss } from "./settings-task-context";
import type { PairingAuthority } from "./pairing-grant";
import { useSettingsOpening } from "./settings-lifetime";
import { InlineRemediation } from "./ui";
import {
  encryptedInput,
  workerRecipient,
  type WorkerRecipient,
} from "./worker-network";
export enum WorkerNetworkAction {
  Prepare = "prepare",
  Import = "import",
  Status = "status",
}
export type ControlWorkerNetwork = (
  machine: string,
  action: WorkerNetworkAction,
  ciphertext: Uint8Array,
  digest: string,
) => Promise<unknown>;
const Context = createContext<ControlWorkerNetwork | undefined>(undefined);
export function WorkerNetworkControlProvider({
  control,
  children,
}: {
  control: ControlWorkerNetwork;
  children: ReactNode;
}) {
  useLocale();
  return <Context.Provider value={control}>{children}</Context.Provider>;
}
export function WorkerNetworkNative({
  machine,
  authority,
  prepared,
}: {
  machine: string;
  authority: PairingAuthority;
  prepared: (recipient: WorkerRecipient) => void;
}) {
  useLocale();
  const control = useContext(Context),
    opening = useSettingsOpening();
  const [busy, setBusy] = useState(false),
    [problem, setProblem] = useProductMessage(""),
    [outcome, setOutcome] = useProductMessage("");
  const [ciphertext, setCiphertext] = useState(""),
    [digest, setDigest] = useState(""),
    [confirm, setConfirm] = useState(false);
  const pending = useRef(false);
  const [originalImport, setOriginalImport] = useState<{
    ciphertext: string;
    digest: string;
  }>();
  const [unconfirmed, setUnconfirmed] = useState(false);
  // Editable inputs are disposable; the dispatched import retains its exact ciphertext/digest.
  useSettingsTaskDismiss(() => {
    setCiphertext("");
    setDigest("");
    setConfirm(false);
  });
  const run = async (action: WorkerNetworkAction) => {
    if (
      !control ||
      pending.current ||
      opening?.disposed ||
      (action === WorkerNetworkAction.Prepare && unconfirmed)
    )
      return;
    const original =
      action === WorkerNetworkAction.Import
        ? (originalImport ?? { ciphertext, digest })
        : undefined;
    const expectedDigest = original?.digest ?? "";
    const input = original
      ? encryptedInput(original.ciphertext)
      : new Uint8Array();
    if (
      !input ||
      (action === WorkerNetworkAction.Import &&
        ((!originalImport && !confirm) ||
          !/^[0-9a-f]{64}$/.test(expectedDigest)))
    ) {
      setProblem(ownedMessage("worker-network-native.extra.0590779bd8e2"));
      return;
    }
    if (original) setOriginalImport(original);
    pending.current = true;
    setBusy(true);
    setProblem("");
    setOutcome("");
    try {
      const result = opening
        ? await opening.native(() =>
            control(machine, action, input, expectedDigest),
          )
        : await control(machine, action, input, expectedDigest);
      if (opening?.disposed) return;
      if (action !== WorkerNetworkAction.Import) {
        const recipient = workerRecipient(result, authority, machine);
        if (!recipient) throw new Error("recipient");
        prepared(recipient);
        if (!originalImport) setUnconfirmed(false);
        setOutcome(ownedMessage("worker-network-native.extra.9b693b5af315"));
      } else {
        const row = result as Record<string, unknown>;
        if (
          row.version !== 1 ||
          row.ciphertext_digest !== expectedDigest ||
          typeof row.generation !== "string" ||
          !/^[1-9][0-9]{0,18}$/.test(row.generation)
        )
          throw new Error("import");
        setOutcome(
          ownedMessage("worker-network-native.sentence.49a8a8c21241", {
            v0: row.generation,
          }),
        );
        setCiphertext("");
        setDigest("");
        setConfirm(false);
        setOriginalImport(undefined);
        setUnconfirmed(false);
      }
    } catch {
      if (!opening?.disposed) {
        setUnconfirmed(true);
        setProblem(ownedMessage("worker-network-native.extra.af95af8c4781"));
      }
    } finally {
      pending.current = false;
      if (!opening?.disposed) setBusy(false);
    }
  };
  // The encrypted import is one semantic form. Pointer, Enter in the digest
  // input and form submission reach this callback, which rechecks the exact
  // disabled conditions of the Import button (busy, or not yet confirmed
  // without a retained original). The textarea keeps Enter as a newline.
  const importFormId = useId();
  const submitImport = () => {
    if (busy || (!originalImport && !confirm)) return;
    void run(WorkerNetworkAction.Import);
  };
  return (
    <section
      aria-label={copy(
        "worker-network-native.thisComputerSWorkerNetwork_ddbb96",
      )}
    >
      <h4>
        {copy("worker-network-native.thisComputerSMatchingWorker_0db01d")}
      </h4>
      <p>
        {copy("worker-network-native.theseControlsUseOnlyTheAlready_4de670")}
      </p>
      {!control ? (
        <p>{copy("worker-network-native.openTheTrustedDesktopAppTo_dce615")}</p>
      ) : (
        <>
          <SettingsActionButton
            icon={SettingsActionIcon.Inspect}
            type="button"
            disabled={busy || unconfirmed}
            onClick={() => void run(WorkerNetworkAction.Prepare)}
          >
            {copy("worker-network-native.prepareProtectedRecipient_5b5127")}
          </SettingsActionButton>
          <SettingsActionButton
            icon={SettingsActionIcon.Retry}
            type="button"
            disabled={busy}
            onClick={() => void run(WorkerNetworkAction.Status)}
          >
            {copy(
              "worker-network-native.inspectOriginalPublicRecipient_579cd7",
            )}
          </SettingsActionButton>
          <form
            id={importFormId}
            className="worker-network-import"
            noValidate
            onKeyDown={guardEnterSubmission}
            onSubmit={(event) => {
              if (consumeFormSubmit(event)) submitImport();
            }}
          >
            <label>
              {copy(
                "worker-network-native.encryptedBundleToImportBase64_db23d6",
              )}
              <textarea
                rows={4}
                maxLength={131072}
                spellCheck={false}
                disabled={busy || Boolean(originalImport)}
                value={ciphertext}
                onChange={(event) => {
                  setCiphertext(event.target.value);
                  setConfirm(false);
                }}
              />
            </label>
            <label>
              {copy(
                "worker-network-native.separatelyAuthenticatedDigest_be070a",
              )}
              <input
                maxLength={64}
                disabled={busy || Boolean(originalImport)}
                value={digest}
                onChange={(event) => {
                  setDigest(event.target.value);
                  setConfirm(false);
                }}
              />
            </label>
            <label>
              <input
                type="checkbox"
                disabled={busy || Boolean(originalImport)}
                checked={confirm}
                onChange={(event) => setConfirm(event.target.checked)}
              />
              {copy(
                "worker-network-native.iVerifiedThisDigestIndependentlyAnd_638722",
              )}
            </label>
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              type="submit"
              disabled={busy || (!originalImport && !confirm)}
            >
              {originalImport
                ? copy(
                    "worker-network-native.retryOriginalEncryptedImport_46c8e9",
                  )
                : copy(
                    "worker-network-native.importConfirmedEncryptedConfiguration_30d15e",
                  )}
            </SettingsActionButton>
          </form>
        </>
      )}
      {outcome ? <p role="status">{outcome}</p> : null}
      {problem ? (
        <InlineRemediation
          summary={
            <>
              <p>{problem}</p>
              <p>{copy("worker-network-native.manualRecheck")}</p>
            </>
          }
        />
      ) : null}
    </section>
  );
}
