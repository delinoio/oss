// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { useEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { EntityKind, ResourceService } from "@delinoio/delidev-api-client";
import { useRunnerRemediation } from "./runner-remediation";
import { copy } from "./localization";
import { Problem } from "./ui";

// Explicit original-identity read only. MachineSettings retains all inspection,
// editing, confirmation and mutation authority in its existing controller.
export function RunnerTaskRemediation({ machineId, disabled = false, visible = true, active = true, onPending }: { machineId: string; disabled?: boolean; visible?: boolean; active?: boolean; onPending?: (value: boolean) => void }) {
  const open = useRunnerRemediation({ active }), transport = useTransport();
  const current = useRef({ machineId, transport, mounted: false, active, epoch: 0, pending: false });
  if (current.current.machineId !== machineId || current.current.transport !== transport || current.current.active !== active) { current.current.epoch++; current.current.pending = false; }
  current.current.active = active; current.current.machineId = machineId; current.current.transport = transport;
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [invalid, setInvalid] = useState(false);
  useEffect(() => { current.current.mounted = true; return () => { current.current.mounted = false; current.current.epoch++; }; }, []);
  useEffect(() => { setBusy(false); setError(undefined); setInvalid(false); }, [machineId, transport, active]);
  const retainedPending = Boolean(open?.pendingFor?.(machineId));
  const otherRunnerLocked = Boolean(open?.locked && !retainedPending);
  useEffect(() => { onPending?.(retainedPending); }, [retainedPending, onPending]);
  const read = async () => {
    if (!open || !active || current.current.pending || disabled || otherRunnerLocked) return;
    const owner = { machineId, transport, epoch: current.current.epoch };
    current.current.pending = true;
    const live = () => current.current.mounted && current.current.active && current.current.machineId === owner.machineId && current.current.transport === owner.transport && current.current.epoch === owner.epoch;
    setBusy(true); setError(undefined); setInvalid(false);
    try {
      const { resource } = await createClient(ResourceService, transport).getResource({ kind: EntityKind.MACHINE, id: machineId });
      if (!live()) return;
      if (!resource || resource.id !== machineId || resource.kind !== EntityKind.MACHINE || resource.schemaVersion !== 1 || resource.revision <= 0n) { setInvalid(true); return; }
      try {
        if (resource.documentJson.byteLength > 1 << 20) throw new Error();
        const value: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(resource.documentJson));
        if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error();
      } catch { setInvalid(true); return; }
      open(resource);
    } catch (failure) { if (live()) setError(failure); }
    finally { if (live()) { current.current.pending = false; setBusy(false); } }
  };
  if (!open) return null;
  const readableIdentity = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(machineId);
  return <>{open.body}{visible && readableIdentity ? <div><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={busy || disabled || otherRunnerLocked} onClick={() => void read()}>{copy("execution-startup.runnerRemedy")}</SettingsActionButton>{otherRunnerLocked ? <p>{copy("execution-startup.runnerOwnershipLocked")}</p> : null}<Problem error={error} />{invalid ? <p role="alert">{copy("execution-startup.runnerReadInvalid")}</p> : null}</div> : null}</>;
}
