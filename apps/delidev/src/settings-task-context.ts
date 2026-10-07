// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useId, useLayoutEffect, useRef } from "react";

export enum SettingsDialogSize { Confirmation = "confirmation", Form = "form", Wide = "wide" }
export enum SettingsDialogFocus { Input = "input", Heading = "heading", Cancel = "cancel" }
export interface SettingsTaskPresentation { title: string; size: SettingsDialogSize; focus?: SettingsDialogFocus }
export interface SettingsTaskContextValue {
  visible: boolean;
  dismiss: () => void;
  dismissWithClose: (idleClose: () => void) => void;
  actions: HTMLElement | null;
  stepTarget: HTMLElement | null;
  activeStep?: string;
  stepId?: string;
  onDismiss: (id: string, action?: () => void) => void;
  present: (id: string, presentation?: SettingsTaskPresentation) => void;
  retireOpener: (removed: (opener: HTMLElement) => boolean) => void;
}
export const SettingsTaskContext = createContext<SettingsTaskContextValue | undefined>(undefined);

export function useSettingsTaskVisible() { return useContext(SettingsTaskContext)?.visible ?? true; }
export function useSettingsTaskDismiss(action: () => void) {
  const task = useContext(SettingsTaskContext), id = useId(), latest = useRef(action);
  latest.current = action;
  const register = task?.onDismiss;
  useLayoutEffect(() => { register?.(id, () => latest.current()); return () => register?.(id); }, [id, register]);
}

export function useCloseSettingsTask(close: () => void) { const task = useContext(SettingsTaskContext); return task?.stepId ? close : task?.dismiss ?? close; }
export function useInSettingsTask() { return Boolean(useContext(SettingsTaskContext)); }
