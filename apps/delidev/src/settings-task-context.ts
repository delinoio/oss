// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useId,
  useLayoutEffect,
  useRef,
} from "react";

import { EntranceTarget } from "./entrance-motion";

export enum SettingsDialogSize {
  Confirmation = "confirmation",
  Form = "form",
  Wide = "wide",
}
export enum SettingsDialogFocus {
  Input = "input",
  Heading = "heading",
  Cancel = "cancel",
  Close = "close",
}
export interface SettingsTaskPresentation {
  title: string;
  subtitle?: string;
  /** Omitted by nested steps that inherit the enclosing task window size. */
  size?: SettingsDialogSize;
  focus?: SettingsDialogFocus;
}
export interface SettingsTaskContextValue {
  visible: boolean;
  dismiss: () => void;
  dismissWithClose: (idleClose: () => void, force?: boolean) => void;
  actions: HTMLElement | null;
  stepTarget: HTMLElement | null;
  activeStep?: string;
  stepId?: string;
  onDismiss: (id: string, action?: () => void) => void;
  present: (id: string, presentation?: SettingsTaskPresentation) => void;
  retireOpener: (removed: (opener: HTMLElement) => boolean) => void;
}
/**
 * Set by a Settings category that opted in to task entrance motion. Every
 * Settings task opened inside that category plays one entrance per opening.
 * Tasks owned by the desktop shell sit outside this tree and opt in through
 * their own Modal heading entrance instead.
 */
export const SettingsTaskEntranceContext = createContext<
  EntranceTarget | undefined
>(undefined);
export const SettingsTaskContext = createContext<
  SettingsTaskContextValue | undefined
>(undefined);

export function useSettingsTaskVisible() {
  return useContext(SettingsTaskContext)?.visible ?? true;
}
export function useSettingsTaskDismiss(action: () => void) {
  const task = useContext(SettingsTaskContext),
    id = useId(),
    latest = useRef(action);
  latest.current = action;
  const register = task?.onDismiss;
  useLayoutEffect(() => {
    register?.(id, () => latest.current());
    return () => register?.(id);
  }, [id, register]);
}

export function useCloseSettingsTask(close: () => void) {
  const task = useContext(SettingsTaskContext);
  return task?.stepId ? close : (task?.dismiss ?? close);
}
export function useInSettingsTask() {
  return Boolean(useContext(SettingsTaskContext));
}
