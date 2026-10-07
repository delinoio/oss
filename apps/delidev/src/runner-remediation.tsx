// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useContext, useLayoutEffect, useMemo, useRef, useState, useId, type ReactNode } from "react";
import type { Resource } from "@delinoio/delidev-api-client";
import { MachineSettingsView, useMachineSettingsController, type MachineSettingsController } from "./machine-settings";
import { SettingsTaskDialog, SettingsDialogFocus, SettingsDialogSize } from "./settings-task";
import { SettingsLifetime } from "./settings-lifetime";
import { MutationIntents } from "./mutation";
import { validRunnerObservation } from "./runner-observation";
import { copy, useLocale } from "./localization";
import type { PairingAuthority } from "./pairing-grant";
interface RemediationOwner {
  request: (resource: Resource, presenter: string) => boolean;
  presenter?: string;
  release: (presenter: string) => void;
  controller?: MachineSettingsController;
  active: boolean;
  authority?: PairingAuthority;
}
const Context = createContext<RemediationOwner | undefined>(undefined);
function ControllerOwner({ initial, active, publish }: { initial: Resource; active: boolean; publish: (controller: MachineSettingsController) => void }) {
  const controller = useMachineSettingsController(initial, active);
  useLayoutEffect(() => publish(controller), [controller, publish]);
  return null;
}
/** One original inspection owner per connection; presentation may close without
 * erasing path drafts, acknowledged jobs or uncertain original requests. */
export function RunnerRemediationProvider({ children, active, authority }: { children: ReactNode; active: boolean; authority?: PairingAuthority }) {
  const [presenter, setPresenter] = useState<string>();
  const [selected, setSelected] = useState<Resource>();
  const [controller, setController] = useState<MachineSettingsController>();
  const original = useRef<MachineSettingsController | undefined>(undefined); original.current = controller;
  const selectedRef = useRef(selected); selectedRef.current = selected;
  const request = useCallback((resource: Resource, source: string) => {
    if (!active || !validRunnerObservation(resource)) return false;
    const previous = selectedRef.current;
    if (previous?.id === resource.id) { setPresenter(source); return true; }
    if (previous && (!original.current || original.current.locked)) return false;
    selectedRef.current = resource;
    original.current = undefined;
    setController(undefined);
    setSelected(resource);
    setPresenter(source);
    return true;
  }, [active]);
  const release = useCallback((source: string) => setPresenter(current => current === source ? undefined : current), []);
  useLayoutEffect(() => { if (!active) { setPresenter(undefined); setSelected(undefined); setController(undefined); } }, [active]);
  const value = useMemo(() => ({ request, release, presenter, controller, active, authority }), [request, release, presenter, controller, active, authority]);
  return <Context.Provider value={value}>{children}{selected ? <SettingsLifetime key={selected.id}>{() => <MutationIntents><ControllerOwner initial={selected} active={active && Boolean(presenter)} publish={setController} /></MutationIntents>}</SettingsLifetime> : null}</Context.Provider>;
}
export type RunnerRemediationOpener = ((resource: Resource) => void) & { body: ReactNode; close: () => void; locked: boolean; pending: boolean; pendingFor: (machineId: string) => boolean };
/** Render `opener.body` within the caller's original task. A nested Step shares
 * its presentation and focus, while the inspection controller stays retained. */
export function useRunnerRemediation({ compact = true, authority }: { compact?: boolean; authority?: PairingAuthority } = {}): RunnerRemediationOpener | undefined {
  useLocale();
  const owner = useContext(Context), presenter = useId();
  const [requested, setRequested] = useState<string>();
  const lastRequested = useRef<string | undefined>(undefined);
  const close = useCallback(() => { setRequested(undefined); owner?.release(presenter); }, [owner?.release, presenter]);
  useLayoutEffect(() => () => owner?.release(presenter), [owner?.release, presenter]);
  const open = useCallback((resource: Resource) => { if (owner?.request(resource, presenter)) { lastRequested.current = resource.id; setRequested(resource.id); } }, [owner?.request, presenter]);
  const controller = owner?.controller;
  const body = requested && owner?.presenter === presenter && owner?.active && controller?.initial.id === requested ? <SettingsTaskDialog title={copy("settings.inspectInstalledHarnesses_45e943")} size={SettingsDialogSize.Wide} focus={SettingsDialogFocus.Heading} close={close} onDismiss={close}><MachineSettingsView controller={controller} active={owner.active} authority={authority ?? owner.authority} close={close} compact={compact} /></SettingsTaskDialog> : null;
  return owner ? Object.assign(open, { body, close, locked: Boolean(controller?.locked && controller.initial.id !== lastRequested.current), pending: Boolean(controller?.locked && controller.initial.id === lastRequested.current), pendingFor: (machineId: string) => Boolean(controller?.locked && controller.initial.id === machineId) }) : undefined;
}
